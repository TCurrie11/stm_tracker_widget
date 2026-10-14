// stm_tracker_widget: live STM bus and métro departures for KDE Plasma 6
// https://github.com/TCurrie11/stm_tracker_widget
//
// File:    internal/server/http.go
// Purpose: The widget's JSON API on localhost.
// Author:  TCurrie11 (https://github.com/TCurrie11)
// Version: 0.1.0
// Created: 2026-10-14
// Updated: 2026-10-14
// License: GPL-2.0-or-later

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

func (s *Service) needFeed(handler func(http.ResponseWriter, *http.Request, *gtfs.Feed)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		feed := s.Feed()
		if feed == nil {
			msg := "schedule still loading"
			if err := s.scheduleError(); err != nil {
				msg = "schedule unavailable: " + err.Error()
			}
			w.Header().Set("Retry-After", "10")
			writeError(w, http.StatusServiceUnavailable, msg)
			return
		}
		handler(w, r, feed)
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

func (s *Service) handleDepartures(w http.ResponseWriter, r *http.Request, feed *gtfs.Feed) {
	query := r.URL.Query()
	stopID := query.Get("stop")
	if stopID == "" {
		writeError(w, http.StatusBadRequest, "stop is required")
		return
	}
	stopInfo, ok := feed.Stops[stopID]
	if !ok {
		writeError(w, http.StatusNotFound, "no stop with ID "+strconv.Quote(stopID))
		return
	}
	perDirection := 3
	if v := query.Get("n"); v != "" {
		var err error
		if perDirection, err = strconv.Atoi(v); err != nil || perDirection < 1 || perDirection > 10 {
			writeError(w, http.StatusBadRequest, "n must be 1 to 10")
			return
		}
	}
	routes := splitList(query.Get("routes"))

	s.Watch(stopID, routes)
	now := s.clock()
	live := s.live(now)
	response := departuresResponse{
		Stop:    toStopJSON(feed, stopInfo),
		Now:     now.In(feed.TimeZone),
		Live:    liveJSON{State: "schedule", Reason: live.Reason, Note: live.Note},
		Routes:  []groupJSON{},
		Credits: credits(s.Agency.Name),
	}
	if live.Snapshot != nil {
		response.Live.State = "live"
		if t := live.Snapshot.FeedTime; !t.IsZero() {
			t = t.In(feed.TimeZone)
			response.Live.FeedTime = &t
		}
	}
	for _, group := range arrivals.Board(feed, live.Snapshot, stopID, routes, now, perDirection, realtime.DefaultThresholds) {
		groupOut := groupJSON{Route: toRouteJSON(group.Route), Direction: group.Direction, Headsign: group.Headsign}
		for _, departure := range group.Departures {
			groupOut.Departures = append(groupOut.Departures, toDepartureJSON(departure, now, feed))
		}
		response.Routes = append(response.Routes, groupOut)
	}
	writeJSON(w, http.StatusOK, response)
}

func toDepartureJSON(departure arrivals.Departure, now time.Time, feed *gtfs.Feed) departureJSON {
	out := departureJSON{
		TripID:    departure.TripID,
		Scheduled: departure.Scheduled.In(feed.TimeZone),
		Expected:  departure.Expected.In(feed.TimeZone),
		Minutes:   int(departure.Expected.Sub(now).Minutes()),
		Status:    string(departure.Status),
		Realtime:  departure.Predicted,
		Frequency: departure.Frequency,
		Occupancy: departure.Occupancy.Level(),
		VehicleID: departure.VehicleID,
	}
	if departure.Predicted {
		out.Delay = int(departure.Delay.Seconds())
	}
	if departure.StopsAway >= 0 {
		out.StopsAway = &departure.StopsAway
	}
	return out
}

func toRouteJSON(route gtfs.Route) routeJSON {
	mode := "other"
	switch route.Type {
	case 0:
		mode = "tram"
	case 1:
		mode = "metro"
	case 2:
		mode = "rail"
	case 3:
		mode = "bus"
	}
	return routeJSON{ID: route.ID, ShortName: route.ShortName, LongName: route.LongName, Mode: mode, Color: route.Color}
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

func toStopJSON(feed *gtfs.Feed, stop *gtfs.Stop) stopJSON {
	out := stopJSON{ID: stop.ID, Code: stop.Code, Name: stop.Name, Station: stop.IsStation,
		Lat: stop.Lat, Lon: stop.Lon, Routes: []routeJSON{}}
	for _, route := range feed.RoutesAt(stop.ID) {
		out.Routes = append(out.Routes, toRouteJSON(route))
	}
	return out
}

func (s *Service) handleSearch(w http.ResponseWriter, r *http.Request, feed *gtfs.Feed) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeError(w, http.StatusBadRequest, "q is required")
		return
	}
	out := []stopJSON{}
	for _, stop := range feed.SearchStops(query, 25) {
		out = append(out, toStopJSON(feed, stop))
	}
	writeJSON(w, http.StatusOK, map[string]any{"stops": out})
}

// handleStop is for the settings page: it lists a saved stop's routes
// without counting as the widget watching it.
func (s *Service) handleStop(w http.ResponseWriter, r *http.Request, feed *gtfs.Feed) {
	stop, ok := feed.Stops[r.PathValue("id")]
	if !ok {
		writeError(w, http.StatusNotFound, "no stop with ID "+strconv.Quote(r.PathValue("id")))
		return
	}
	writeJSON(w, http.StatusOK, toStopJSON(feed, stop))
}

// handleStations lists parent stations, which in STM's feed are exactly
// the métro stations; the widget offers them as a "home station" list.
func (s *Service) handleStations(w http.ResponseWriter, r *http.Request, feed *gtfs.Feed) {
	out := []stopJSON{}
	for _, stop := range feed.Stops {
		if stop.IsStation {
			if entry := toStopJSON(feed, stop); len(entry.Routes) > 0 {
				out = append(out, entry)
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

func (s *Service) handleNearby(w http.ResponseWriter, r *http.Request, feed *gtfs.Feed) {
	query := r.URL.Query()
	radius := 500.0
	if v := query.Get("radius"); v != "" {
		var err error
		if radius, err = strconv.ParseFloat(v, 64); err != nil || radius <= 0 || radius > 5000 {
			writeError(w, http.StatusBadRequest, "radius must be 1 to 5000 meters")
			return
		}
	}
	var place geo.Place
	switch addr := strings.TrimSpace(query.Get("address")); {
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
	case query.Get("lat") != "" && query.Get("lon") != "":
		lat, err1 := strconv.ParseFloat(query.Get("lat"), 64)
		lon, err2 := strconv.ParseFloat(query.Get("lon"), 64)
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
	for _, nearby := range feed.NearbyStops(place.Lat, place.Lon, radius, 20) {
		entry := toStopJSON(feed, nearby.Stop)
		meters := float64(int(nearby.Meters + 0.5))
		entry.Meters = &meters
		out = append(out, entry)
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
	if wait := time.Second - time.Since(s.lastGeocode); wait > 0 {
		select {
		case <-ctx.Done():
			return geo.Place{}, ctx.Err()
		case <-time.After(wait):
		}
	}
	defer func() { s.lastGeocode = time.Now() }()
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
	status := statusJSON{Agency: s.Agency.ID}
	if feed := s.Feed(); feed != nil {
		status.Schedule = scheduleJSON{Loaded: true, Stops: len(feed.Stops), Routes: len(feed.Routes)}
	}
	s.mu.Lock()
	status.Schedule.FileTime = timePtr(s.loadedMod)
	if s.scheduleErr != nil {
		status.Schedule.Error = s.scheduleErr.Error()
	}
	realtimeStatus := realtimeJSON{
		Polling:   s.polling,
		Reason:    s.plan.Reason,
		Watching:  len(s.watches),
		FetchedAt: timePtr(s.fetchedAt),
	}
	if s.polling == "paused" {
		realtimeStatus.ResumeAt = timePtr(s.plan.WakeAt)
	}
	if s.snap != nil {
		realtimeStatus.FeedTime = timePtr(s.snap.FeedTime)
	}
	if s.liveErr != nil {
		realtimeStatus.Error = s.liveErr.Error() // already free of the API key
	}
	s.mu.Unlock()
	if s.Fetcher != nil && s.Fetcher.Budget != nil {
		realtimeStatus.RequestsUsed, realtimeStatus.RequestsStop = s.Fetcher.Budget.Used()
	}
	status.Realtime = realtimeStatus
	writeJSON(w, http.StatusOK, status)
}
