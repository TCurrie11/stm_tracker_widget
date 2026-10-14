// Package server runs transitd as a background service: it keeps the
// schedule loaded, polls the realtime feeds while they are useful, and
// answers the widget from memory over HTTP on localhost.
//
// Polling follows the widget. Each departures request marks that stop and
// those routes as watched; poll.Decide then pauses overnight, and once the
// widget has asked for nothing in IdleAfter (widget closed, screen locked
// for the night) the service stops fetching altogether. Widget requests
// never reach the agency directly, however often they come.
package server

import (
	"context"
	"errors"
	"log"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TCurrie11/stm_tracker_widget/internal/config"
	"github.com/TCurrie11/stm_tracker_widget/internal/geo"
	"github.com/TCurrie11/stm_tracker_widget/internal/gtfs"
	"github.com/TCurrie11/stm_tracker_widget/internal/poll"
	"github.com/TCurrie11/stm_tracker_widget/internal/realtime"
)

// Service is the long-running state behind the HTTP API.
type Service struct {
	Agency       config.Agency
	PollInterval time.Duration
	WakeLead     time.Duration
	IdleAfter    time.Duration

	GTFSPath   string
	GTFSMaxAge time.Duration // re-download the schedule when older than this; 0 = never

	Fetcher  *realtime.Fetcher // nil = schedule only
	Geocoder geo.Geocoder      // nil = no address search
	Log      *log.Logger

	now  func() time.Time // for tests
	once sync.Once
	feed atomic.Pointer[gtfs.Feed]
	kick chan struct{}

	mu          sync.Mutex
	snap        *realtime.Snapshot
	fetchedAt   time.Time // last successful fetch
	attemptAt   time.Time // last fetch attempt
	liveErr     error     // last fetch error, cleared on success
	plan        poll.Plan
	polling     string // "active", "paused", "idle", "off", "starting"
	watches     map[string]watch
	scheduleErr error
	loadedMod   time.Time // modification time of the loaded zip

	geoMu   sync.Mutex
	geoLast time.Time
}

type watch struct {
	poll.Watch
	lastAsked time.Time
}

// maxSleep caps every wait in the loops. Go's timers don't advance while a
// laptop is suspended, so a single 7-hour sleep until morning service would
// wake up hours late; short sleeps re-check the wall clock instead.
const maxSleep = 5 * time.Minute

func (s *Service) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// init sets up internal state on first use, from whichever of Run and the
// HTTP handlers gets there first.
func (s *Service) init() {
	s.once.Do(func() {
		s.kick = make(chan struct{}, 1)
		s.watches = map[string]watch{}
		s.polling = "starting"
		if s.Log == nil {
			s.Log = log.New(os.Stderr, "", log.LstdFlags)
		}
	})
}

// Feed returns the loaded schedule, or nil while it is still loading.
func (s *Service) Feed() *gtfs.Feed { return s.feed.Load() }

// Run keeps the schedule fresh and polls realtime feeds until ctx ends.
func (s *Service) Run(ctx context.Context) {
	s.init()
	go s.scheduleLoop(ctx)
	for {
		wait := s.step(ctx)
		select {
		case <-ctx.Done():
			return
		case <-s.kick:
		case <-time.After(min(wait, maxSleep)):
		}
	}
}

func (s *Service) wake() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// ---- schedule ----

func (s *Service) scheduleLoop(ctx context.Context) {
	for {
		wait := time.Hour
		if !s.refreshSchedule(ctx) {
			wait = maxSleep // nothing usable yet: retry sooner
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// refreshSchedule downloads the zip if it is missing or old, and (re)loads
// it if it changed on disk, which also picks up a manual `transitd fetch`.
// It reports whether a schedule is loaded.
func (s *Service) refreshSchedule(ctx context.Context) bool {
	fi, err := os.Stat(s.GTFSPath)
	stale := err == nil && s.GTFSMaxAge > 0 && s.clock().Sub(fi.ModTime()) > s.GTFSMaxAge
	if errors.Is(err, os.ErrNotExist) || stale {
		s.Log.Printf("downloading schedule from %s", s.Agency.GTFSURL)
		dctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		n, derr := gtfs.Download(dctx, s.Agency.GTFSURL, s.GTFSPath)
		cancel()
		if derr != nil {
			// A stale schedule is still far better than none: keep it.
			s.Log.Printf("schedule download failed: %v", derr)
			s.setScheduleErr(derr)
		} else {
			s.Log.Printf("saved %.1f MB to %s", float64(n)/1e6, s.GTFSPath)
		}
		fi, err = os.Stat(s.GTFSPath)
	}
	if err != nil {
		if s.scheduleError() == nil {
			s.setScheduleErr(err)
		}
		return s.Feed() != nil
	}

	s.mu.Lock()
	unchanged := fi.ModTime().Equal(s.loadedMod)
	s.mu.Unlock()
	if unchanged && s.Feed() != nil {
		return true
	}
	loc, err := time.LoadLocation(s.Agency.Timezone)
	if err != nil {
		s.setScheduleErr(err)
		return s.Feed() != nil
	}
	start := time.Now()
	f, err := gtfs.Load(s.GTFSPath, loc)
	if err != nil {
		s.Log.Printf("loading schedule: %v", err)
		s.setScheduleErr(err)
		return s.Feed() != nil
	}
	s.feed.Store(f)
	s.mu.Lock()
	s.loadedMod, s.scheduleErr = fi.ModTime(), nil
	s.mu.Unlock()
	s.Log.Printf("loaded %d stops, %d routes in %s", len(f.Stops), len(f.Routes),
		time.Since(start).Round(time.Millisecond))
	s.wake()
	return true
}

func (s *Service) setScheduleErr(err error) {
	s.mu.Lock()
	s.scheduleErr = err
	s.mu.Unlock()
}

func (s *Service) scheduleError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scheduleErr
}

// ---- realtime ----

// Watch records that the widget is showing stop/routes right now. A watch
// the service wasn't already polling for wakes the loop, so live data
// starts flowing within seconds rather than at the next tick.
func (s *Service) Watch(stop string, routes []string) {
	s.init()
	routes = slices.Clone(routes)
	slices.Sort(routes)
	key := stop + "|" + strings.Join(routes, ",")
	s.mu.Lock()
	_, known := s.watches[key]
	s.watches[key] = watch{poll.Watch{Stop: stop, Routes: routes}, s.clock()}
	s.mu.Unlock()
	if !known {
		s.wake()
	}
}

// activeWatches drops watches the widget stopped asking about.
func (s *Service) activeWatches(now time.Time) []poll.Watch {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []poll.Watch
	for k, w := range s.watches {
		if now.Sub(w.lastAsked) > s.IdleAfter {
			delete(s.watches, k)
			continue
		}
		out = append(out, w.Watch)
	}
	return out
}

// step makes one polling decision, fetching if it is time, and returns
// how long to sleep before the next one.
func (s *Service) step(ctx context.Context) time.Duration {
	s.init()
	f := s.Feed()
	if f == nil {
		return maxSleep // the schedule loop wakes us once it is loaded
	}
	if s.Fetcher == nil || s.Agency.TripUpdatesURL == "" {
		s.setPolling("off", poll.Plan{Reason: "no realtime feed configured"})
		return maxSleep
	}
	now := s.clock()
	watches := s.activeWatches(now)
	if len(watches) == 0 {
		s.setPolling("idle", poll.Plan{Reason: "widget not open"})
		return maxSleep
	}
	plan := poll.Decide(f, watches, now, s.WakeLead)
	if !plan.Active {
		s.setPolling("paused", plan)
		return plan.WakeAt.Sub(now)
	}
	s.setPolling("active", plan)

	s.mu.Lock()
	since := now.Sub(s.attemptAt)
	s.mu.Unlock()
	if since < s.PollInterval {
		return s.PollInterval - since
	}
	s.fetch(ctx, now)
	return s.PollInterval
}

func (s *Service) setPolling(state string, p poll.Plan) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.polling != state {
		msg := state
		if p.Reason != "" {
			msg += " (" + p.Reason + ")"
		}
		s.Log.Printf("polling %s", msg)
	}
	s.polling, s.plan = state, p
}

// fetch downloads both feeds into a new snapshot. On failure the previous
// snapshot is kept; requests stop using it once it goes stale.
func (s *Service) fetch(ctx context.Context, now time.Time) {
	s.mu.Lock()
	s.attemptAt = now
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tu, err := s.Fetcher.Fetch(ctx, "trip updates", s.Agency.TripUpdatesURL)
	if err != nil {
		s.mu.Lock()
		repeat := s.liveErr != nil && s.liveErr.Error() == err.Error()
		var bo *realtime.BackoffError
		if !errors.As(err, &bo) || s.liveErr == nil {
			s.liveErr = err // keep the cause, not "backing off", for the widget
		}
		s.mu.Unlock()
		if !repeat && !errors.As(err, &bo) {
			s.Log.Printf("realtime: %v", err)
		}
		return
	}
	var vp *realtime.Feed
	if s.Agency.VehiclePositionsURL != "" {
		// Positions only add stops-away and occupancy; predictions alone
		// are still worth showing if this one fails.
		if vp, err = s.Fetcher.Fetch(ctx, "vehicle positions", s.Agency.VehiclePositionsURL); err != nil {
			s.Log.Printf("realtime: %v", err)
		}
	}
	snap := realtime.NewSnapshot(tu, vp)
	s.mu.Lock()
	if s.liveErr != nil {
		s.Log.Printf("realtime: recovered")
	}
	s.snap, s.fetchedAt, s.liveErr = snap, s.clock(), nil
	s.mu.Unlock()
}

// freshFor is how long a snapshot is trusted after it was fetched. Older
// predictions are worse than the schedule, so they are dropped.
func (s *Service) freshFor() time.Duration {
	return max(3*s.PollInterval, 3*time.Minute)
}

// Live is what the widget needs to know about live data for one response.
type Live struct {
	Snapshot *realtime.Snapshot // nil = schedule only
	// Reason says why Snapshot is nil, as a code the widget can act on:
	// no_feed, no_key, unauthorized, budget, error, no_service, starting.
	Reason string
	Note   string // the same in words, for logs and debugging
}

// live returns the snapshot to use now, or the reason there is none.
func (s *Service) live(now time.Time) Live {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snap != nil && now.Sub(s.fetchedAt) <= s.freshFor() {
		return Live{Snapshot: s.snap}
	}
	switch {
	case s.Fetcher == nil || s.Agency.TripUpdatesURL == "":
		return Live{Reason: "no_feed", Note: "no live data for this agency"}
	case s.liveErr != nil:
		reason, note := describe(s.liveErr)
		return Live{Reason: reason, Note: note}
	case s.polling == "paused":
		return Live{Reason: "no_service", Note: s.plan.Reason}
	}
	return Live{Reason: "starting", Note: "waiting for live data"}
}

// describe turns a fetch error into a reason code and a short note. The
// full error is in the log.
func describe(err error) (reason, note string) {
	switch {
	case errors.Is(err, realtime.ErrNoKey):
		return "no_key", "no API key configured"
	case errors.Is(err, realtime.ErrUnauthorized):
		return "unauthorized", "API key rejected"
	case errors.Is(err, realtime.ErrBudgetExhausted):
		return "budget", "daily request limit reached"
	}
	return "error", "live data unavailable"
}
