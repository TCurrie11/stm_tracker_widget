// Package gtfs loads a static GTFS feed (stops, routes, trips, schedules)
// into memory and answers "what leaves this stop next?".
package gtfs

import "time"

// Stop is a bus stop, a metro platform, or a metro station (parent).
type Stop struct {
	ID        string
	Code      string // number printed on STM signs (stop_code)
	Name      string
	Lat, Lon  float64
	Parent    string // parent_station, set on metro platforms
	IsStation bool   // location_type == 1
}

// Route is a bus or metro line.
type Route struct {
	ID        string
	ShortName string // "24", "1"
	LongName  string // "Sherbrooke", "Verte"
	Type      int    // 1 = metro, 3 = bus
	Color     string
}

// IsMetro reports whether the route is a subway line (GTFS route_type 1).
func (r Route) IsMetro() bool { return r.Type == 1 }

type trip struct {
	routeIdx  int32
	serviceID string
	headsign  string
	direction int8 // 0 or 1, -1 if missing
}

// stopTime is one scheduled visit of a trip at a stop. Times are seconds
// after the service day's midnight and may exceed 24h (GTFS allows 25:10:00).
type stopTime struct {
	tripIdx   int32
	departure int32
	sequence  int32
}

// frequency describes a headway-based trip (STM uses these for the metro):
// the trip's stop_times are a template that repeats every Headway seconds
// between Start and End.
type frequency struct {
	start, end, headway int32
}

type service struct {
	days       [7]bool // indexed by time.Weekday
	start, end string  // YYYYMMDD, inclusive
}

// Feed is an indexed, read-only static GTFS feed. Safe for concurrent reads.
type Feed struct {
	Stops  map[string]*Stop
	Routes []Route

	routeByID map[string]int32
	trips     []trip
	tripIDs   []string
	tripByID  map[string]int32
	// stop_times grouped by stop ID, which is the only way we query them.
	byStop map[string][]stopTime
	// first scheduled departure of each trip, used to offset frequency trips
	tripStart []int32
	freqs     map[int32][]frequency

	services map[string]service
	added    map[string]map[string]bool // date -> serviceID (calendar_dates type 1)
	removed  map[string]map[string]bool // date -> serviceID (calendar_dates type 2)
	children map[string][]string        // station ID -> platform stop IDs
	Loc      *time.Location
}
