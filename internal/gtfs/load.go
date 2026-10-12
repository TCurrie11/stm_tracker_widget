package gtfs

import (
	"archive/zip"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// Load reads a GTFS zip file. loc is the agency time zone
// (America/Montreal for STM); schedule times are interpreted in it.
func Load(path string, loc *time.Location) (*Feed, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("open gtfs zip: %w", err)
	}
	defer zr.Close()
	return load(&zr.Reader, loc)
}

func load(zr *zip.Reader, loc *time.Location) (*Feed, error) {
	f := &Feed{
		Stops:     map[string]*Stop{},
		routeByID: map[string]int32{},
		tripByID:  map[string]int32{},
		byStop:    map[string][]stopTime{},
		freqs:     map[int32][]frequency{},
		services:  map[string]service{},
		added:     map[string]map[string]bool{},
		removed:   map[string]map[string]bool{},
		children:  map[string][]string{},
		Loc:       loc,
	}
	files := map[string]*zip.File{}
	for _, zf := range zr.File {
		files[zf.Name] = zf
	}

	// Order matters: trips need routes, stop_times need trips.
	steps := []struct {
		name     string
		required bool
		fn       func(row) error
	}{
		{"stops.txt", true, f.addStop},
		{"routes.txt", true, f.addRoute},
		{"trips.txt", true, f.addTrip},
		{"stop_times.txt", true, f.addStopTime},
		{"calendar.txt", false, f.addCalendar},
		{"calendar_dates.txt", false, f.addCalendarDate},
		{"frequencies.txt", false, f.addFrequency},
	}
	for _, s := range steps {
		zf, ok := files[s.name]
		if !ok {
			if s.required {
				return nil, fmt.Errorf("gtfs: missing %s", s.name)
			}
			continue
		}
		if s.name == "stop_times.txt" {
			f.tripStart = make([]int32, len(f.trips))
			for i := range f.tripStart {
				f.tripStart[i] = -1
			}
		}
		if err := readCSV(zf, s.fn); err != nil {
			return nil, fmt.Errorf("gtfs %s: %w", s.name, err)
		}
	}
	if len(f.services) == 0 && len(f.added) == 0 {
		return nil, errors.New("gtfs: neither calendar.txt nor calendar_dates.txt has data")
	}
	for id, s := range f.Stops {
		if s.Parent != "" {
			f.children[s.Parent] = append(f.children[s.Parent], id)
		}
	}
	return f, nil
}

// row gives access to a CSV record by column name.
type row struct {
	cols map[string]int
	rec  []string
}

func (r row) get(name string) string {
	i, ok := r.cols[name]
	if !ok || i >= len(r.rec) {
		return ""
	}
	return strings.TrimSpace(r.rec[i])
}

func readCSV(zf *zip.File, fn func(row) error) error {
	rc, err := zf.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	cr := csv.NewReader(rc)
	cr.LazyQuotes = true
	cr.FieldsPerRecord = -1
	cr.ReuseRecord = true
	header, err := cr.Read()
	if err != nil {
		return err
	}
	cols := map[string]int{}
	for i, h := range header {
		cols[strings.TrimSpace(strings.TrimPrefix(h, "\ufeff"))] = i
	}
	line := 1
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			return nil
		}
		line++
		if err != nil {
			return fmt.Errorf("line %d: %w", line, err)
		}
		if err := fn(row{cols, rec}); err != nil {
			return fmt.Errorf("line %d: %w", line, err)
		}
	}
}

func (f *Feed) addStop(r row) error {
	lat, _ := strconv.ParseFloat(r.get("stop_lat"), 64)
	lon, _ := strconv.ParseFloat(r.get("stop_lon"), 64)
	id := r.get("stop_id")
	f.Stops[id] = &Stop{
		ID: id, Code: r.get("stop_code"), Name: r.get("stop_name"),
		Lat: lat, Lon: lon, Parent: r.get("parent_station"),
		IsStation: r.get("location_type") == "1",
	}
	return nil
}

func (f *Feed) addRoute(r row) error {
	t, _ := strconv.Atoi(r.get("route_type"))
	id := r.get("route_id")
	f.routeByID[id] = int32(len(f.Routes))
	f.Routes = append(f.Routes, Route{
		ID: id, ShortName: r.get("route_short_name"), LongName: r.get("route_long_name"),
		Type: t, Color: r.get("route_color"),
	})
	return nil
}

func (f *Feed) addTrip(r row) error {
	ri, ok := f.routeByID[r.get("route_id")]
	if !ok {
		return nil // trip for an unknown route; skip rather than fail
	}
	dir := int8(-1)
	switch r.get("direction_id") {
	case "0":
		dir = 0
	case "1":
		dir = 1
	}
	f.tripByID[r.get("trip_id")] = int32(len(f.trips))
	f.tripIDs = append(f.tripIDs, r.get("trip_id"))
	f.trips = append(f.trips, trip{routeIdx: ri, serviceID: r.get("service_id"),
		headsign: r.get("trip_headsign"), direction: dir})
	return nil
}

func (f *Feed) addStopTime(r row) error {
	ti, ok := f.tripByID[r.get("trip_id")]
	if !ok {
		return nil
	}
	t := r.get("departure_time")
	if t == "" {
		t = r.get("arrival_time")
	}
	if t == "" {
		return nil // untimed stop; GTFS allows interpolation, we just skip it
	}
	secs, err := ParseGTFSTime(t)
	if err != nil {
		return err
	}
	seq, _ := strconv.Atoi(r.get("stop_sequence"))
	stop := r.get("stop_id")
	f.byStop[stop] = append(f.byStop[stop], stopTime{tripIdx: ti, departure: secs, sequence: int32(seq)})
	if f.tripStart[ti] < 0 || secs < f.tripStart[ti] {
		f.tripStart[ti] = secs
	}
	return nil
}

func (f *Feed) addCalendar(r row) error {
	var s service
	for i, name := range []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"} {
		s.days[i] = r.get(name) == "1"
	}
	s.start, s.end = r.get("start_date"), r.get("end_date")
	f.services[r.get("service_id")] = s
	return nil
}

func (f *Feed) addCalendarDate(r row) error {
	date, id := r.get("date"), r.get("service_id")
	m := f.added
	if r.get("exception_type") == "2" {
		m = f.removed
	}
	if m[date] == nil {
		m[date] = map[string]bool{}
	}
	m[date][id] = true
	return nil
}

func (f *Feed) addFrequency(r row) error {
	ti, ok := f.tripByID[r.get("trip_id")]
	if !ok {
		return nil
	}
	start, err := ParseGTFSTime(r.get("start_time"))
	if err != nil {
		return err
	}
	end, err := ParseGTFSTime(r.get("end_time"))
	if err != nil {
		return err
	}
	h, err := strconv.Atoi(r.get("headway_secs"))
	if err != nil || h <= 0 {
		return fmt.Errorf("bad headway_secs %q", r.get("headway_secs"))
	}
	f.freqs[ti] = append(f.freqs[ti], frequency{start, end, int32(h)})
	return nil
}

// ParseGTFSTime parses "H:MM:SS" or "HH:MM:SS" into seconds after midnight.
// Hours may be 24 or more for trips that run past midnight.
func ParseGTFSTime(s string) (int32, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("bad time %q", s)
	}
	var n [3]int
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 {
			return 0, fmt.Errorf("bad time %q", s)
		}
		n[i] = v
	}
	if n[1] > 59 || n[2] > 59 {
		return 0, fmt.Errorf("bad time %q", s)
	}
	return int32(n[0]*3600 + n[1]*60 + n[2]), nil
}
