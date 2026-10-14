// Command transitd is the local backend for the STM transit widget.
//
//	transitd serve                      run the background service the widget talks to
//
// The other commands explore the schedule and live data from a terminal:
//
//	transitd fetch                      download STM's schedule data
//	transitd search berri               find stop / station IDs by name or code
//	transitd next -stop 52345 -routes 24 -n 3
//	transitd nearby "1500 rue Berri"    stops near an address
//	transitd plan -stop 52345 -routes 24  would the service poll right now?
//	transitd live -stop 52345 -routes 24  next departures with live data
//
// Settings come from ~/.config/stm-transit/config.json (optional; STM is
// the built-in default) and the STM_API_KEY environment variable.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/TCurrie11/stm_tracker_widget/internal/arrivals"
	"github.com/TCurrie11/stm_tracker_widget/internal/config"
	"github.com/TCurrie11/stm_tracker_widget/internal/geo"
	"github.com/TCurrie11/stm_tracker_widget/internal/gtfs"
	"github.com/TCurrie11/stm_tracker_widget/internal/poll"
	"github.com/TCurrie11/stm_tracker_widget/internal/realtime"
	"github.com/TCurrie11/stm_tracker_widget/internal/server"
)

// cfg is loaded once in main; the CLI uses the first agency.
var cfg config.Config

func agency() config.Agency { return cfg.Agencies[0] }

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	if cfg, err = config.Load(config.DefaultPath()); err != nil {
		fmt.Fprintln(os.Stderr, "transitd: config:", err)
		os.Exit(1)
	}
	switch os.Args[1] {
	case "serve":
		err = cmdServe(os.Args[2:])
	case "fetch":
		err = cmdFetch(os.Args[2:])
	case "search":
		err = cmdSearch(os.Args[2:])
	case "next":
		err = cmdNext(os.Args[2:])
	case "nearby":
		err = cmdNearby(os.Args[2:])
	case "plan":
		err = cmdPlan(os.Args[2:])
	case "live":
		err = cmdLive(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "transitd:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  transitd serve  [-gtfs FILE] [-listen 127.0.0.1:8765]
  transitd fetch [-url URL]
  transitd search [-gtfs FILE] <name or stop code>
  transitd next   [-gtfs FILE] -stop ID [-routes 24,165] [-n 3] [-at 2026-10-12T08:00]
  transitd nearby [-gtfs FILE] [-radius 500] <address>
  transitd plan   [-gtfs FILE] -stop ID [-routes 24] [-at 2026-10-12T23:30]
  transitd live   [-gtfs FILE] -stop ID [-routes 24] [-n 3] [-tu FILE -vp FILE -at TIME]`)
	os.Exit(2)
}

// defaultGTFSPath is ~/.cache/stm-transit/<agency>.zip (respects XDG_CACHE_HOME).
func defaultGTFSPath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "stm-transit", agency().ID+".zip")
}

func cmdFetch(args []string) error {
	fs := flag.NewFlagSet("fetch", flag.ExitOnError)
	url := fs.String("url", agency().GTFSURL, "GTFS zip URL")
	out := fs.String("o", defaultGTFSPath(), "where to save it")
	fs.Parse(args)

	n, err := gtfs.Download(context.Background(), *url, *out)
	if err != nil {
		return err
	}
	fmt.Printf("saved %.1f MB to %s\n", float64(n)/1e6, *out)
	return nil
}

// gtfsMaxAge is when the service re-downloads the schedule. STM publishes
// a new zip every few weeks; a weekly check keeps up without being wasteful.
const gtfsMaxAge = 7 * 24 * time.Hour

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	path := fs.String("gtfs", defaultGTFSPath(), "GTFS zip (downloaded if missing)")
	listen := fs.String("listen", cfg.Listen, "address to listen on (loopback only)")
	fs.Parse(args)
	if err := checkLoopback(*listen); err != nil {
		return err
	}

	a := agency()
	loc, err := time.LoadLocation(a.Timezone)
	if err != nil {
		return err
	}
	logger := log.New(os.Stderr, "", 0) // journald adds timestamps
	if isTerminal() {
		logger.SetFlags(log.LstdFlags)
	}
	svc := &server.Service{
		Agency:       a,
		PollInterval: cfg.PollInterval.D(),
		WakeLead:     cfg.WakeLead.D(),
		IdleAfter:    cfg.IdleAfter.D(),
		GTFSPath:     *path,
		GTFSMaxAge:   gtfsMaxAge,
		Geocoder:     newGeocoder(),
		Log:          logger,
	}
	if a.TripUpdatesURL != "" {
		svc.Fetcher = newFetcher(loc)
		if a.Key() == "" {
			logger.Printf("no API key (set %s): serving schedule times only", a.APIKeyEnv)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           svc.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go svc.Run(ctx)
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	logger.Printf("listening on http://%s", ln.Addr())
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	logger.Printf("stopped")
	return nil
}

// checkLoopback refuses to listen beyond this machine: the API has no
// authentication and the service spends the user's API quota.
func checkLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("listen address %q: %w", addr, err)
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("listen address %q: only loopback addresses (127.0.0.1, ::1, localhost) are allowed", addr)
	}
	return nil
}

func loadFeed(path string) (*gtfs.Feed, error) {
	loc, err := time.LoadLocation(agency().Timezone)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	f, err := gtfs.Load(path, loc)
	if err != nil {
		return nil, fmt.Errorf("%w (run `transitd fetch` first?)", err)
	}
	fmt.Fprintf(os.Stderr, "loaded %d stops, %d routes in %s\n",
		len(f.Stops), len(f.Routes), time.Since(start).Round(time.Millisecond))
	return f, nil
}

func cmdSearch(args []string) error {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	path := fs.String("gtfs", defaultGTFSPath(), "GTFS zip")
	fs.Parse(args)
	if fs.NArg() == 0 {
		usage()
	}
	f, err := loadFeed(*path)
	if err != nil {
		return err
	}
	for _, s := range f.SearchStops(strings.Join(fs.Args(), " "), 25) {
		kind := "stop"
		if s.IsStation {
			kind = "station"
		}
		fmt.Printf("%-8s %-12s code %-6s %s\n", kind, s.ID, s.Code, s.Name)
	}
	return nil
}

func cmdNext(args []string) error {
	fs := flag.NewFlagSet("next", flag.ExitOnError)
	path := fs.String("gtfs", defaultGTFSPath(), "GTFS zip")
	stop := fs.String("stop", "", "stop or station ID (see `transitd search`)")
	routes := fs.String("routes", "", "comma-separated route numbers, empty = all")
	n := fs.Int("n", 3, "departures per direction")
	at := fs.String("at", "", "pretend it is this local time (2006-01-02T15:04)")
	fs.Parse(args)
	if *stop == "" {
		usage()
	}
	f, err := loadFeed(*path)
	if err != nil {
		return err
	}
	if _, ok := f.Stops[*stop]; !ok {
		return fmt.Errorf("no stop with ID %q", *stop)
	}
	now, err := parseAt(*at, f)
	if err != nil {
		return err
	}
	filter := splitRoutes(*routes)

	fmt.Printf("%s — %s\n", f.Stops[*stop].Name, now.In(f.Loc).Format("Mon 15:04"))
	groups := f.Next(*stop, now, *n, filter)
	if len(groups) == 0 {
		fmt.Println("  no departures in the next 12 hours")
	}
	for _, g := range groups {
		fmt.Printf("  %-4s → %-30s", g.Route.ShortName, g.Headsign)
		for _, d := range g.Departures {
			mins := int(d.Time.Sub(now).Minutes())
			fmt.Printf("  %s (%d min)", d.Time.Format("15:04"), mins)
		}
		if len(g.Departures) > 0 && g.Departures[0].FromFrequency {
			fmt.Print("  [scheduled frequency]")
		}
		fmt.Println()
	}
	return nil
}

func parseAt(at string, f *gtfs.Feed) (time.Time, error) {
	if at == "" {
		return time.Now(), nil
	}
	return time.ParseInLocation("2006-01-02T15:04", at, f.Loc)
}

func splitRoutes(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

func newGeocoder() geo.Geocoder {
	if cfg.Geocoder.Provider == "google" {
		return &geo.Google{Key: cfg.Geocoder.GoogleKey, Bias: geo.Montreal}
	}
	return &geo.Nominatim{Contact: cfg.Geocoder.Contact, Bias: geo.Montreal}
}

func cmdNearby(args []string) error {
	fs := flag.NewFlagSet("nearby", flag.ExitOnError)
	path := fs.String("gtfs", defaultGTFSPath(), "GTFS zip")
	radius := fs.Float64("radius", 500, "search radius in meters")
	fs.Parse(args)
	if fs.NArg() == 0 {
		usage()
	}
	f, err := loadFeed(*path)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	place, err := newGeocoder().Geocode(ctx, strings.Join(fs.Args(), " "))
	if err != nil {
		return err
	}
	fmt.Printf("%s (%.5f, %.5f)\n", place.Label, place.Lat, place.Lon)
	for _, s := range f.NearbyStops(place.Lat, place.Lon, *radius, 20) {
		var names []string
		for _, r := range f.RoutesAt(s.ID) {
			names = append(names, r.ShortName)
		}
		fmt.Printf("  %4.0f m  %-12s %-40s routes %s\n", s.Meters, s.ID, s.Name, strings.Join(names, " "))
	}
	return nil
}

func cmdPlan(args []string) error {
	fs := flag.NewFlagSet("plan", flag.ExitOnError)
	path := fs.String("gtfs", defaultGTFSPath(), "GTFS zip")
	stop := fs.String("stop", "", "stop or station ID")
	routes := fs.String("routes", "", "comma-separated route numbers, empty = all")
	at := fs.String("at", "", "pretend it is this local time (2006-01-02T15:04)")
	fs.Parse(args)
	if *stop == "" {
		usage()
	}
	f, err := loadFeed(*path)
	if err != nil {
		return err
	}
	now, err := parseAt(*at, f)
	if err != nil {
		return err
	}
	p := poll.Decide(f, []poll.Watch{{Stop: *stop, Routes: splitRoutes(*routes)}}, now, cfg.WakeLead.D())
	if p.Active {
		fmt.Printf("polling every %s (%s)\n", cfg.PollInterval.D(), p.Reason)
	} else {
		fmt.Printf("paused, %s; resume at %s\n", p.Reason, p.WakeAt.In(f.Loc).Format("Mon 15:04"))
	}
	return nil
}

// newFetcher builds a realtime fetcher for the first agency, with the daily
// budget kept in ~/.cache/stm-transit/budget-<agency>.json.
func newFetcher(loc *time.Location) *realtime.Fetcher {
	a := agency()
	return &realtime.Fetcher{
		Auth:   realtime.Auth{Key: a.Key(), Header: a.APIKeyHeader, Param: a.APIKeyParam},
		Budget: realtime.NewBudget(filepath.Join(filepath.Dir(defaultGTFSPath()), "budget-"+a.ID+".json"), a.DailyRequestLimit, loc),
	}
}

// liveSnapshot fetches both feeds, or reads them from files (-tu/-vp) for
// offline testing. On failure it returns nil and a short reason: the board
// then shows schedule times, as the widget will.
func liveSnapshot(f *gtfs.Feed, tuFile, vpFile string) (*realtime.Snapshot, string) {
	if tuFile != "" || vpFile != "" {
		var feeds [2]*realtime.Feed
		for i, p := range []string{tuFile, vpFile} {
			if p == "" {
				continue
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return nil, err.Error()
			}
			if feeds[i], err = realtime.Decode(b); err != nil {
				return nil, p + ": " + err.Error()
			}
		}
		return realtime.NewSnapshot(feeds[0], feeds[1]), ""
	}
	a := agency()
	if a.TripUpdatesURL == "" {
		return nil, "no realtime feed configured"
	}
	fetcher := newFetcher(f.Loc)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tu, err := fetcher.Fetch(ctx, "trip updates", a.TripUpdatesURL)
	if err != nil {
		return nil, err.Error()
	}
	var vp *realtime.Feed
	if a.VehiclePositionsURL != "" {
		// Positions only add stops-away and occupancy; predictions alone are
		// still worth showing if this one fails.
		if vp, err = fetcher.Fetch(ctx, "vehicle positions", a.VehiclePositionsURL); err != nil {
			fmt.Fprintln(os.Stderr, "note:", err)
		}
	}
	if used, stop := fetcher.Budget.Used(); stop > 0 {
		fmt.Fprintf(os.Stderr, "requests today: %d of %d\n", used, stop)
	}
	return realtime.NewSnapshot(tu, vp), ""
}

func cmdLive(args []string) error {
	fs := flag.NewFlagSet("live", flag.ExitOnError)
	path := fs.String("gtfs", defaultGTFSPath(), "GTFS zip")
	stop := fs.String("stop", "", "stop or station ID")
	routes := fs.String("routes", "", "comma-separated route numbers, empty = all")
	n := fs.Int("n", 3, "departures per direction")
	tuFile := fs.String("tu", "", "read trip updates from this file instead of fetching")
	vpFile := fs.String("vp", "", "read vehicle positions from this file instead of fetching")
	at := fs.String("at", "", "pretend it is this local time (with -tu/-vp)")
	fs.Parse(args)
	if *stop == "" {
		usage()
	}
	f, err := loadFeed(*path)
	if err != nil {
		return err
	}
	if _, ok := f.Stops[*stop]; !ok {
		return fmt.Errorf("no stop with ID %q", *stop)
	}
	now, err := parseAt(*at, f)
	if err != nil {
		return err
	}
	snap, problem := liveSnapshot(f, *tuFile, *vpFile)

	fmt.Printf("%s — %s\n", f.Stops[*stop].Name, now.In(f.Loc).Format("Mon 15:04"))
	if problem != "" {
		fmt.Printf("  Live data paused — showing schedule (%s)\n", problem)
	} else if !snap.FeedTime.IsZero() {
		fmt.Printf("  live data from %s\n", snap.FeedTime.In(f.Loc).Format("15:04:05"))
	}
	groups := arrivals.Board(f, snap, *stop, splitRoutes(*routes), now, *n, realtime.DefaultThresholds)
	if len(groups) == 0 {
		fmt.Println("  no departures in the next 12 hours")
	}
	color := isTerminal()
	for _, g := range groups {
		fmt.Printf("\n  %s → %s\n", g.Route.ShortName, g.Headsign)
		for _, d := range g.Departures {
			fmt.Println("    " + formatDeparture(d, now, f.Loc, color))
		}
	}
	return nil
}

func formatDeparture(d arrivals.Departure, now time.Time, loc *time.Location, color bool) string {
	mins := int(d.Expected.Sub(now).Minutes())
	line := fmt.Sprintf("%s  in %2d min", d.Expected.In(loc).Format("15:04"), mins)
	paint := ""
	switch d.Status {
	case realtime.Late:
		line += fmt.Sprintf("  +%d late", int(d.Delay.Round(time.Minute).Minutes()))
		paint = "\x1b[31m" // red
	case realtime.Early:
		line += fmt.Sprintf("  %d early", int(d.Delay.Round(time.Minute).Minutes()))
		paint = "\x1b[33m" // orange/yellow
	case realtime.OnTime:
		line += "  on time"
	case realtime.Scheduled:
		line += "  scheduled"
		paint = "\x1b[2m" // dim
	case realtime.Canceled:
		line = d.Scheduled.In(loc).Format("15:04") + "  canceled"
		paint = "\x1b[9;2m" // struck through
	case realtime.Skipped:
		line = d.Scheduled.In(loc).Format("15:04") + "  not stopping here (detour)"
		paint = "\x1b[9;2m"
	}
	switch {
	case d.StopsAway == 0:
		line += "  · at the stop"
	case d.StopsAway == 1:
		line += "  · next stop"
	case d.StopsAway > 1:
		line += fmt.Sprintf("  · %d stops away", d.StopsAway)
	}
	if lvl := d.Occupancy.Level(); lvl > 0 {
		bars := strings.Repeat("▮", lvl) + strings.Repeat("▯", 3-lvl)
		label := [...]string{"", "seats free", "few seats", "standing room"}[lvl]
		line += "  " + bars + " " + label
	}
	if color && paint != "" {
		return paint + line + "\x1b[0m"
	}
	return line
}

func isTerminal() bool {
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
