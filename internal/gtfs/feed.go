// stm_tracker_widget: live STM bus and métro departures for KDE Plasma 6
// https://github.com/TCurrie11/stm_tracker_widget
//
// File:    internal/gtfs/feed.go
// Purpose: Types for the in-memory GTFS schedule.
// Author:  TCurrie11 (https://github.com/TCurrie11)
// Version: 0.1.0
// Created: 2026-10-12
// Updated: 2026-10-14
// License: GPL-2.0-or-later

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
	routeIndex int32
	serviceID  string
	headsign   string
	direction  int8 // 0 or 1, -1 if missing
}

// stopTime is one scheduled visit of a trip at a stop. Times are seconds
// after the service day's midnight and may exceed 24h (GTFS allows 25:10:00).
type stopTime struct {
	tripIndex int32
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

	routeIndexByID map[string]int32
	trips          []trip
	tripIDs        []string
	tripIndexByID  map[string]int32
	// stop_times grouped by stop ID, which is the only way we query them.
	stopTimesByStop map[string][]stopTime
	// first scheduled departure of each trip, used to offset frequency trips
	tripFirstDeparture []int32
	frequenciesByTrip  map[int32][]frequency

	services           map[string]service
	servicesAddedOn    map[string]map[string]bool // date -> serviceID (calendar_dates type 1)
	servicesRemovedOn  map[string]map[string]bool // date -> serviceID (calendar_dates type 2)
	platformsByStation map[string][]string        // station ID -> platform stop IDs
	TimeZone           *time.Location
}
