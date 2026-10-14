package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TCurrie11/stm_tracker_widget/internal/config"
	"github.com/TCurrie11/stm_tracker_widget/internal/gtfs"
	"github.com/TCurrie11/stm_tracker_widget/internal/gtfs/gtfstest"
	"github.com/TCurrie11/stm_tracker_widget/internal/realtime"
)

// The fixture's route 24 Est leaves stop 52345 at 08:00, 08:15, 08:30 and
// 08:45 on weekdays, and the Orange line runs through STATION-BERRI from
// 05:30. Monday 2026-10-12 is a weekday.

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }
func (c *clock) set(f *gtfs.Feed, hhmm string) {
	t, err := time.ParseInLocation("2006-01-02 15:04", "2026-10-12 "+hhmm, f.Loc)
	if err != nil {
		panic(err)
	}
	c.t = t
}

func newService(t *testing.T, hhmm string) (*Service, *clock) {
	t.Helper()
	loc, _ := time.LoadLocation("America/Montreal")
	f, err := gtfs.LoadBytes(gtfstest.Zip(), loc)
	if err != nil {
		t.Fatal(err)
	}
	c := &clock{}
	c.set(f, hhmm)
	s := &Service{
		Agency:       config.Agency{ID: "stm", Name: "Société de transport de Montréal"},
		PollInterval: time.Minute,
		WakeLead:     30 * time.Minute,
		IdleAfter:    10 * time.Minute,
		Log:          log.New(io.Discard, "", 0),
		now:          c.now,
	}
	s.init()
	s.feed.Store(f)
	return s, c
}

// agencyFeed serves STM's captured trip updates as a stand-in for the
// realtime API, counting requests; status overrides the response code.
func agencyFeed(t *testing.T, status *atomic.Int32) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	gz, err := os.ReadFile("../realtime/testdata/tripupdates.pb.gz")
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if code := status.Load(); code != 0 {
			w.WriteHeader(int(code))
			return
		}
		w.Write(body)
	}))
	t.Cleanup(ts.Close)
	return ts, &hits
}

func withRealtime(t *testing.T, s *Service) (*atomic.Int32, *atomic.Int32) {
	var status atomic.Int32
	ts, hits := agencyFeed(t, &status)
	s.Agency.TripUpdatesURL = ts.URL
	s.Fetcher = &realtime.Fetcher{Auth: realtime.Auth{Key: "secret", Header: "apikey"}}
	return hits, &status
}

func get(t *testing.T, s *Service, url string, out any) int {
	t.Helper()
	req := httptest.NewRequest("GET", url, nil)
	req.Host = "127.0.0.1:8765"
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("%s: %v\n%s", url, err, rec.Body)
		}
	}
	return rec.Code
}

func TestDeparturesScheduleOnly(t *testing.T) {
	s, _ := newService(t, "07:55")
	var resp departuresResponse
	if code := get(t, s, "/v1/departures?stop=52345&routes=24&n=2", &resp); code != 200 {
		t.Fatalf("status %d", code)
	}
	if resp.Stop.Name != "Sherbrooke / Saint-Denis" || resp.Live.State != "schedule" {
		t.Errorf("stop %q, live %+v", resp.Stop.Name, resp.Live)
	}
	if len(resp.Routes) != 1 {
		t.Fatalf("got %d groups, want 1", len(resp.Routes))
	}
	g := resp.Routes[0]
	if g.Route.ShortName != "24" || g.Route.Mode != "bus" || g.Headsign != "Est" || len(g.Departures) != 2 {
		t.Fatalf("group %+v", g)
	}
	d := g.Departures[0]
	if d.Minutes != 5 || d.Status != "scheduled" || d.Realtime || d.StopsAway != nil || d.Occupancy != 0 {
		t.Errorf("first departure %+v", d)
	}
	if got := d.Expected.Format("15:04 -07:00"); got != "08:00 -04:00" {
		t.Errorf("expected time %s, want local time with offset", got)
	}
	if resp.Credits == "" {
		t.Error("missing data attribution")
	}
}

func TestDeparturesMetroIsFrequency(t *testing.T) {
	s, _ := newService(t, "07:55")
	var resp departuresResponse
	get(t, s, "/v1/departures?stop=STATION-BERRI", &resp)
	if len(resp.Routes) == 0 {
		t.Fatal("no metro departures")
	}
	g := resp.Routes[0]
	if g.Route.Mode != "metro" || !g.Departures[0].Frequency {
		t.Errorf("got %+v", g)
	}
}

func TestDeparturesBadRequests(t *testing.T) {
	s, _ := newService(t, "07:55")
	for url, want := range map[string]int{
		"/v1/departures":                     400,
		"/v1/departures?stop=nope":           404,
		"/v1/departures?stop=52345&n=99":     400,
		"/v1/stops":                          400,
		"/v1/stops/nearby":                   400,
		"/v1/stops/nearby?lat=x&lon=1":       400,
		"/v1/stops/nearby?address=somewhere": 502, // no geocoder configured
	} {
		var e map[string]string
		if code := get(t, s, url, &e); code != want || e["error"] == "" {
			t.Errorf("%s: %d %v, want %d with an error message", url, code, e, want)
		}
	}
}

func TestLoadingSchedule(t *testing.T) {
	s, _ := newService(t, "07:55")
	s.feed.Store(nil)
	if code := get(t, s, "/v1/departures?stop=52345", nil); code != 503 {
		t.Errorf("got %d, want 503 while loading", code)
	}
	var st statusJSON
	if code := get(t, s, "/v1/status", &st); code != 200 || st.Schedule.Loaded {
		t.Errorf("status %d %+v", code, st)
	}
}

func TestOnlyLocalHosts(t *testing.T) {
	s, _ := newService(t, "07:55")
	for host, want := range map[string]int{
		"127.0.0.1:8765":    200,
		"localhost:8765":    200,
		"[::1]:8765":        200,
		"evil.example":      403,
		"evil.example:8765": 403,
		"192.168.1.5:8765":  403,
	} {
		req := httptest.NewRequest("GET", "/v1/status", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("Host %s: got %d, want %d", host, rec.Code, want)
		}
	}
}

func TestStopSearch(t *testing.T) {
	s, _ := newService(t, "07:55")
	var resp struct{ Stops []stopJSON }
	get(t, s, "/v1/stops?q=berri", &resp)
	if len(resp.Stops) == 0 || resp.Stops[0].ID != "STATION-BERRI" || len(resp.Stops[0].Routes) == 0 {
		t.Errorf("got %+v", resp.Stops)
	}
	get(t, s, "/v1/stops/nearby?lat=45.5185&lon=-73.5690&radius=200", &resp)
	if len(resp.Stops) == 0 || resp.Stops[0].ID != "52345" || resp.Stops[0].Meters == nil {
		t.Errorf("nearby: got %+v", resp.Stops)
	}
}

func TestPollingFollowsWidget(t *testing.T) {
	s, c := newService(t, "07:55")
	hits, _ := withRealtime(t, s)
	ctx := context.Background()

	s.step(ctx)
	if hits.Load() != 0 || s.polling != "idle" {
		t.Fatalf("before any request: %d fetches, polling %s", hits.Load(), s.polling)
	}

	var resp departuresResponse
	get(t, s, "/v1/departures?stop=52345&routes=24", &resp)
	if resp.Live.State != "schedule" || resp.Live.Note != "waiting for live data" {
		t.Errorf("first response live %+v", resp.Live)
	}
	select {
	case <-s.kick:
	default:
		t.Error("a new watch should wake the poll loop")
	}
	s.step(ctx)
	if hits.Load() != 1 || s.polling != "active" {
		t.Fatalf("after request: %d fetches, polling %s", hits.Load(), s.polling)
	}
	get(t, s, "/v1/departures?stop=52345&routes=24", &resp)
	if resp.Live.State != "live" || resp.Live.FeedTime == nil {
		t.Errorf("after fetch live %+v", resp.Live)
	}

	// Repeated requests and wake-ups don't fetch faster than the interval.
	c.t = c.t.Add(20 * time.Second)
	if wait := s.step(ctx); hits.Load() != 1 || wait != 40*time.Second {
		t.Errorf("early step: %d fetches, wait %s", hits.Load(), wait)
	}
	c.t = c.t.Add(40 * time.Second)
	s.step(ctx)
	if hits.Load() != 2 {
		t.Errorf("after interval: %d fetches, want 2", hits.Load())
	}

	// Widget closed: no requests for longer than IdleAfter.
	c.t = c.t.Add(11 * time.Minute)
	s.step(ctx)
	if hits.Load() != 2 || s.polling != "idle" {
		t.Errorf("idle: %d fetches, polling %s", hits.Load(), s.polling)
	}
	if l := s.live(c.t); l.Snapshot != nil {
		t.Error("an 11-minute-old snapshot should not be used")
	}
}

func TestPausedOvernight(t *testing.T) {
	s, _ := newService(t, "02:00")
	hits, _ := withRealtime(t, s)
	var resp departuresResponse
	get(t, s, "/v1/departures?stop=52345&routes=24", &resp)
	s.step(context.Background())
	if hits.Load() != 0 || s.polling != "paused" {
		t.Fatalf("%d fetches, polling %s", hits.Load(), s.polling)
	}
	get(t, s, "/v1/departures?stop=52345&routes=24", &resp)
	if resp.Live.Note != "no service until 08:00" {
		t.Errorf("note %q", resp.Live.Note)
	}
	var st statusJSON
	get(t, s, "/v1/status", &st)
	if st.Realtime.ResumeAt == nil || st.Realtime.ResumeAt.In(s.Feed().Loc).Format("15:04") != "07:30" {
		t.Errorf("status %+v", st.Realtime)
	}
}

func TestRejectedKeyIsExplained(t *testing.T) {
	s, _ := newService(t, "07:55")
	_, status := withRealtime(t, s)
	status.Store(401)
	var resp departuresResponse
	get(t, s, "/v1/departures?stop=52345&routes=24", &resp)
	s.step(context.Background())
	get(t, s, "/v1/departures?stop=52345&routes=24", &resp)
	if resp.Live.State != "schedule" || resp.Live.Note != "API key rejected" {
		t.Errorf("live %+v", resp.Live)
	}
	var st statusJSON
	get(t, s, "/v1/status", &st)
	if st.Realtime.Error == "" || bytes.Contains([]byte(st.Realtime.Error), []byte("secret")) {
		t.Errorf("status error %q", st.Realtime.Error)
	}
}
