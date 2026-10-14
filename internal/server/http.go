package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/TCurrie11/stm_tracker_widget/internal/arrivals"
	"github.com/TCurrie11/stm_tracker_widget/internal/geo"
	"github.com/TCurrie11/stm_tracker_widget/internal/gtfs"
	"github.com/TCurrie11/stm_tracker_widget/internal/realtime"
)

// Handler serves the widget API:
//
//	GET /v1/departures?stop=ID&routes=24,165&n=3   the departure board
//	GET /v1/stops?q=berri                           stops by name or code
//	GET /v1/stops/nearby?address=…&radius=500       stops near an address
//	GET /v1/stops/nearby?lat=…&lon=…                stops near a point
//	GET /v1/stops/{id}                              one stop with its routes
//	GET /v1/stations                                stations (STM: the métro), by name
//	GET /v1/status                                  service health, for the widget and for debugging
//
// Errors are JSON too: {"error": "..."}.
func (s *Service) Handler() http.Handler {
	s.init()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/departures", s.needFeed(s.handleDepartures))
	mux.HandleFunc("GET /v1/stops", s.needFeed(s.handleSearch))
	mux.HandleFunc("GET /v1/stops/nearby", s.needFeed(s.handleNearby))
	mux.HandleFunc("GET /v1/stops/{id}", s.needFeed(s.handleStop))
	mux.HandleFunc("GET /v1/stations", s.needFeed(s.handleStations))
	mux.HandleFunc("GET /v1/status", s.handleStatus)
	return localOnly(mux)
}

// localOnly rejects requests whose Host isn't this machine. Listening on
// 127.0.0.1 keeps other computers out, but a web page open in a browser
// could still reach the port through DNS rebinding (evil.example resolving
// to 127.0.0.1); its requests carry the attacker's host name.
func localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		host = strings.Trim(host, "[]")
		if ip := net.ParseIP(host); !(host == "localhost" || ip != nil && ip.IsLoopback()) {
			writeError(w, http.StatusForbidden, "only local requests are accepted")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Service) needFeed(h func(http.ResponseWriter, *http.Request, *gtfs.Feed)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f := s.Feed()
		if f == nil {
			msg := "schedule still loading"
			if err := s.scheduleError(); err != nil {
				msg = "schedule unavailable: " + err.Error()
			}
			w.Header().Set("Retry-After", "10")
			writeError(w, http.StatusServiceUnavailable, msg)
			return
		}
		h(w, r, f)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// ---- departures ----

type departuresResponse struct {
	Stop    stopJSON    `json:"stop"`
	Now     time.Time   `json:"now"`
	Live    liveJSON    `json:"live"`
	Routes  []groupJSON `json:"routes"`
	Credits string      `json:"credits"` // data attribution the widget must show
}

type liveJSON struct {
	// "live" when realtime data was applied, "schedule" otherwise; Reason
	// then says why as a code (see Live.Reason), Note in words.
	State    string     `json:"state"`
	Reason   string     `json:"reason,omitempty"`
	FeedTime *time.Time `json:"feed_time,omitempty"`
	Note     string     `json:"note,omitempty"`
}

type routeJSON struct {
	ID        string `json:"id"`
	ShortName string `json:"short_name"`
	LongName  string `json:"long_name"`
	Mode      string `json:"mode"`            // "metro", "bus", "rail", "tram" or "other"
	Color     string `json:"color,omitempty"` // hex without '#', from the agency
}

type groupJSON struct {
	Route      routeJSON       `json:"route"`
	Direction  int             `json:"direction"` // 0/1, -1 if the agency doesn't say
	Headsign   string          `json:"headsign"`
	Departures []departureJSON `json:"departures"`
}

type departureJSON struct {
	TripID    string    `json:"trip_id"`
	Scheduled time.Time `json:"scheduled"`
	Expected  time.Time `json:"expected"`
	Minutes   int       `json:"minutes"` // until Expected, rounded down, at Now
	// Status: scheduled, on_time, late, early, canceled or skipped (detour).
	Status    string `json:"status"`
	Delay     int    `json:"delay_seconds"` // Expected − Scheduled; only when realtime
	Realtime  bool   `json:"realtime"`      // Expected is a live prediction
	Frequency bool   `json:"frequency"`     // headway service (metro): "about every N min"
	StopsAway *int   `json:"stops_away"`    // null = unknown, 0 = at the stop
	Occupancy int    `json:"occupancy"`     // 0 unknown, 1 seats free, 2 few seats, 3 standing room
	VehicleID string `json:"vehicle_id,omitempty"`
}

func (s *Service) handleDepartures(w http.ResponseWriter, r *http.Request, f *gtfs.Feed) {
	q := r.URL.Query()
	stopID := q.Get("stop")
	if stopID == "" {
		writeError(w, http.StatusBadRequest, "stop is required")
		return
	}
	stop, ok := f.Stops[stopID]
	if !ok {
		writeError(w, http.StatusNotFound, "no stop with ID "+strconv.Quote(stopID))
		return
	}
	n := 3
	if v := q.Get("n"); v != "" {
		var err error
		if n, err = strconv.Atoi(v); err != nil || n < 1 || n > 10 {
			writeError(w, http.StatusBadRequest, "n must be 1 to 10")
			return
		}
	}
	routes := splitList(q.Get("routes"))

	s.Watch(stopID, routes)
	now := s.clock()
	live := s.live(now)
	resp := departuresResponse{
		Stop:    toStopJSON(f, stop),
		Now:     now.In(f.Loc),
		Live:    liveJSON{State: "schedule", Reason: live.Reason, Note: live.Note},
		Routes:  []groupJSON{},
		Credits: credits(s.Agency.Name),
	}
	if live.Snapshot != nil {
		resp.Live.State = "live"
		if t := live.Snapshot.FeedTime; !t.IsZero() {
			t = t.In(f.Loc)
			resp.Live.FeedTime = &t
		}
	}
	for _, g := range arrivals.Board(f, live.Snapshot, stopID, routes, now, n, realtime.DefaultThresholds) {
		gj := groupJSON{Route: toRouteJSON(g.Route), Direction: g.Direction, Headsign: g.Headsign}
		for _, d := range g.Departures {
			gj.Departures = append(gj.Departures, toDepartureJSON(d, now, f))
		}
		resp.Routes = append(resp.Routes, gj)
	}
	writeJSON(w, http.StatusOK, resp)
}

func toDepartureJSON(d arrivals.Departure, now time.Time, f *gtfs.Feed) departureJSON {
	out := departureJSON{
		TripID:    d.TripID,
		Scheduled: d.Scheduled.In(f.Loc),
		Expected:  d.Expected.In(f.Loc),
		Minutes:   int(d.Expected.Sub(now).Minutes()),
		Status:    string(d.Status),
		Realtime:  d.Predicted,
		Frequency: d.Frequency,
		Occupancy: d.Occupancy.Level(),
		VehicleID: d.VehicleID,
	}
	if d.Predicted {
		out.Delay = int(d.Delay.Seconds())
	}
	if d.StopsAway >= 0 {
		out.StopsAway = &d.StopsAway
	}
	return out
}

func toRouteJSON(r gtfs.Route) routeJSON {
	mode := "other"
	switch r.Type {
	case 0:
		mode = "tram"
	case 1:
		mode = "metro"
	case 2:
		mode = "rail"
	case 3:
		mode = "bus"
	}
	return routeJSON{ID: r.ID, ShortName: r.ShortName, LongName: r.LongName, Mode: mode, Color: r.Color}
}

func credits(agency string) string {
	if agency == "" {
		return ""
	}
	return "Data © " + agency
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ---- stop search ----

type stopJSON struct {
	ID      string      `json:"id"`
	Code    string      `json:"code,omitempty"` // number on the stop sign
	Name    string      `json:"name"`
	Station bool        `json:"station"`
	Lat     float64     `json:"lat"`
	Lon     float64     `json:"lon"`
	Meters  *float64    `json:"meters,omitempty"` // nearby search only
	Routes  []routeJSON `json:"routes"`
}

func toStopJSON(f *gtfs.Feed, s *gtfs.Stop) stopJSON {
	out := stopJSON{ID: s.ID, Code: s.Code, Name: s.Name, Station: s.IsStation,
		Lat: s.Lat, Lon: s.Lon, Routes: []routeJSON{}}
	for _, r := range f.RoutesAt(s.ID) {
		out.Routes = append(out.Routes, toRouteJSON(r))
	}
	return out
}

func (s *Service) handleSearch(w http.ResponseWriter, r *http.Request, f *gtfs.Feed) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeError(w, http.StatusBadRequest, "q is required")
		return
	}
	out := []stopJSON{}
	for _, st := range f.SearchStops(q, 25) {
		out = append(out, toStopJSON(f, st))
	}
	writeJSON(w, http.StatusOK, map[string]any{"stops": out})
}

// handleStop is for the settings page: it lists a saved stop's routes
// without counting as the widget watching it.
func (s *Service) handleStop(w http.ResponseWriter, r *http.Request, f *gtfs.Feed) {
	st, ok := f.Stops[r.PathValue("id")]
	if !ok {
		writeError(w, http.StatusNotFound, "no stop with ID "+strconv.Quote(r.PathValue("id")))
		return
	}
	writeJSON(w, http.StatusOK, toStopJSON(f, st))
}

// handleStations lists parent stations, which in STM's feed are exactly
// the métro stations; the widget offers them as a "home station" list.
func (s *Service) handleStations(w http.ResponseWriter, r *http.Request, f *gtfs.Feed) {
	out := []stopJSON{}
	for _, st := range f.Stops {
		if st.IsStation {
			if sj := toStopJSON(f, st); len(sj.Routes) > 0 {
				out = append(out, sj)
			}
		}
	}
	slices.SortFunc(out, func(a, b stopJSON) int { return strings.Compare(a.Name, b.Name) })
	writeJSON(w, http.StatusOK, map[string]any{"stations": out})
}

type placeJSON struct {
	Label string  `json:"label"`
	Lat   float64 `json:"lat"`
	Lon   float64 `json:"lon"`
}

func (s *Service) handleNearby(w http.ResponseWriter, r *http.Request, f *gtfs.Feed) {
	q := r.URL.Query()
	radius := 500.0
	if v := q.Get("radius"); v != "" {
		var err error
		if radius, err = strconv.ParseFloat(v, 64); err != nil || radius <= 0 || radius > 5000 {
			writeError(w, http.StatusBadRequest, "radius must be 1 to 5000 meters")
			return
		}
	}
	var place geo.Place
	switch addr := strings.TrimSpace(q.Get("address")); {
	case addr != "":
		var err error
		if place, err = s.geocode(r.Context(), addr); err != nil {
			code := http.StatusBadGateway
			if errors.Is(err, geo.ErrNotFound) {
				code = http.StatusNotFound
			}
			s.Log.Printf("geocode: %v", err)
			writeError(w, code, err.Error())
			return
		}
	case q.Get("lat") != "" && q.Get("lon") != "":
		lat, err1 := strconv.ParseFloat(q.Get("lat"), 64)
		lon, err2 := strconv.ParseFloat(q.Get("lon"), 64)
		if err1 != nil || err2 != nil {
			writeError(w, http.StatusBadRequest, "lat and lon must be numbers")
			return
		}
		place = geo.Place{Lat: lat, Lon: lon}
	default:
		writeError(w, http.StatusBadRequest, "address, or lat and lon, is required")
		return
	}
	out := []stopJSON{}
	for _, ns := range f.NearbyStops(place.Lat, place.Lon, radius, 20) {
		sj := toStopJSON(f, ns.Stop)
		m := float64(int(ns.Meters + 0.5))
		sj.Meters = &m
		out = append(out, sj)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"place": placeJSON{Label: place.Label, Lat: place.Lat, Lon: place.Lon},
		"stops": out,
	})
}

// geocode serializes lookups at most one per second, as Nominatim's usage
// policy requires; typing in the settings dialog could otherwise burst.
func (s *Service) geocode(ctx context.Context, addr string) (geo.Place, error) {
	if s.Geocoder == nil {
		return geo.Place{}, errors.New("address search is not configured")
	}
	s.geoMu.Lock()
	defer s.geoMu.Unlock()
	if wait := time.Second - time.Since(s.geoLast); wait > 0 {
		select {
		case <-ctx.Done():
			return geo.Place{}, ctx.Err()
		case <-time.After(wait):
		}
	}
	defer func() { s.geoLast = time.Now() }()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return s.Geocoder.Geocode(ctx, addr)
}

// ---- status ----

type statusJSON struct {
	Agency   string       `json:"agency"`
	Schedule scheduleJSON `json:"schedule"`
	Realtime realtimeJSON `json:"realtime"`
}

type scheduleJSON struct {
	Loaded   bool       `json:"loaded"`
	Stops    int        `json:"stops,omitempty"`
	Routes   int        `json:"routes,omitempty"`
	FileTime *time.Time `json:"file_time,omitempty"`
	Error    string     `json:"error,omitempty"`
}

type realtimeJSON struct {
	// Polling: "active", "paused" (no service soon), "idle" (widget not
	// asking), "off" (no feed or key) or "starting".
	Polling      string     `json:"polling"`
	Reason       string     `json:"reason,omitempty"`
	ResumeAt     *time.Time `json:"resume_at,omitempty"`
	Watching     int        `json:"watching"`
	FetchedAt    *time.Time `json:"fetched_at,omitempty"`
	FeedTime     *time.Time `json:"feed_time,omitempty"`
	Error        string     `json:"error,omitempty"`
	RequestsUsed int        `json:"requests_today"`
	RequestsStop int        `json:"requests_stop_at,omitempty"` // fetching pauses here for the day
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func (s *Service) handleStatus(w http.ResponseWriter, r *http.Request) {
	st := statusJSON{Agency: s.Agency.ID}
	if f := s.Feed(); f != nil {
		st.Schedule = scheduleJSON{Loaded: true, Stops: len(f.Stops), Routes: len(f.Routes)}
	}
	s.mu.Lock()
	st.Schedule.FileTime = timePtr(s.loadedMod)
	if s.scheduleErr != nil {
		st.Schedule.Error = s.scheduleErr.Error()
	}
	rt := realtimeJSON{
		Polling:   s.polling,
		Reason:    s.plan.Reason,
		Watching:  len(s.watches),
		FetchedAt: timePtr(s.fetchedAt),
	}
	if s.polling == "paused" {
		rt.ResumeAt = timePtr(s.plan.WakeAt)
	}
	if s.snap != nil {
		rt.FeedTime = timePtr(s.snap.FeedTime)
	}
	if s.liveErr != nil {
		rt.Error = s.liveErr.Error() // already free of the API key
	}
	s.mu.Unlock()
	if s.Fetcher != nil && s.Fetcher.Budget != nil {
		rt.RequestsUsed, rt.RequestsStop = s.Fetcher.Budget.Used()
	}
	st.Realtime = rt
	writeJSON(w, http.StatusOK, st)
}
