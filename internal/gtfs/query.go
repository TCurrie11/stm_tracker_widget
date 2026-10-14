// stm_tracker_widget: live STM bus and métro departures for KDE Plasma 6
// https://github.com/TCurrie11/stm_tracker_widget
//
// File:    internal/gtfs/query.go
// Purpose: Schedule queries: next departures, stop search, nearby stops.
// Author:  TCurrie11 (https://github.com/TCurrie11)
// Version: 0.1.0
// Created: 2026-10-12
// Updated: 2026-10-14
// License: GPL-2.0-or-later

package gtfs

import (
	"math"
	"sort"
	"strings"
	"time"
)

// Departure is one scheduled departure from a stop.
type Departure struct {
	Time     time.Time
	TripID   string // matches GTFS-Realtime updates
	Headsign string
	// ServiceDate (YYYYMMDD) is the schedule day the trip belongs to, which
	// for after-midnight trips is the previous calendar day. Together with
	// TripID it identifies one run of a trip, as GTFS-Realtime's start_date does.
	ServiceDate string
	StopID      string // the platform/stop the departure is from
	Sequence    int32  // stop_sequence within the trip
	// FromFrequency is true for headway-based trips (the metro): the time
	// is "a train roughly every N minutes", not a specific vehicle.
	FromFrequency bool
}

// DirectionGroup is the next departures for one route in one direction.
type DirectionGroup struct {
	Route      Route
	Direction  int // 0/1, or -1 when the feed has no direction_id
	Headsign   string
	Departures []Departure
}

// horizon limits how far ahead we look; covers the overnight gap in service.
const horizon = 12 * time.Hour

// Next returns, for each route and direction serving stopID, the next
// limit departures at or after now. stopID may be a metro station, in which case
// all of its platforms are included. If routeFilter is non-empty, only those
// route IDs (or short names) are returned.
func (f *Feed) Next(stopID string, now time.Time, limit int, routeFilter []string) []DirectionGroup {
	now = now.In(f.TimeZone)
	servedStopIDs := append([]string{stopID}, f.platformsByStation[stopID]...)
	wantedRoutes := map[int32]bool{}
	for _, name := range routeFilter {
		if index, ok := f.routeIndexByID[name]; ok {
			wantedRoutes[index] = true
			continue
		}
		for index, route := range f.Routes {
			if route.ShortName == name {
				wantedRoutes[int32(index)] = true
			}
		}
	}

	type groupKey struct {
		route     int32
		direction int8
		headsign  string // only used when direction is missing
	}
	departuresByGroup := map[groupKey][]Departure{}
	horizonEnd := now.Add(horizon)

	// A departure at 25:30 on yesterday's service is 01:30 today, so check
	// yesterday; tomorrow covers late-evening queries.
	for dayOffset := -1; dayOffset <= 1; dayOffset++ {
		serviceDay := now.AddDate(0, 0, dayOffset)
		activeServices := f.activeServices(serviceDay)
		dayStart := serviceDayStart(serviceDay, f.TimeZone)
		serviceDate := serviceDay.Format("20060102")
		for _, platformID := range servedStopIDs {
			for _, visit := range f.stopTimesByStop[platformID] {
				tripInfo := f.trips[visit.tripIndex]
				if !activeServices[tripInfo.serviceID] || (len(routeFilter) > 0 && !wantedRoutes[tripInfo.routeIndex]) {
					continue
				}
				key := groupKey{route: tripInfo.routeIndex, direction: tripInfo.direction}
				if tripInfo.direction < 0 {
					key.headsign = tripInfo.headsign
				}
				addDeparture := func(seconds int32, fromFrequency bool) bool {
					departs := dayStart.Add(time.Duration(seconds) * time.Second)
					if departs.Before(now) {
						return true
					}
					if departs.After(horizonEnd) {
						return false
					}
					departuresByGroup[key] = append(departuresByGroup[key], Departure{Time: departs, TripID: f.tripIDs[visit.tripIndex],
						Headsign: tripInfo.headsign, FromFrequency: fromFrequency, ServiceDate: serviceDate,
						StopID: platformID, Sequence: visit.sequence})
					return true
				}
				windows, isFrequencyBased := f.frequenciesByTrip[visit.tripIndex]
				if !isFrequencyBased {
					addDeparture(visit.departure, false)
					continue
				}
				// Expand headway-based trips: the trip's stop times are a
				// template that repeats every headway across each window, so
				// this stop is reached offsetInTrip after each run starts. Only
				// the first limit future runs per window can matter.
				offsetInTrip := visit.departure - f.tripFirstDeparture[visit.tripIndex]
				nowInTemplate := int32(now.Sub(dayStart).Seconds()) - offsetInTrip
				for _, window := range windows {
					firstRun := window.start
					if nowInTemplate > firstRun {
						missedRuns := (nowInTemplate - firstRun + window.headway - 1) / window.headway
						firstRun += missedRuns * window.headway
					}
					for count, runStart := 0, firstRun; count < limit && runStart < window.end; count, runStart = count+1, runStart+window.headway {
						if !addDeparture(runStart+offsetInTrip, true) {
							break
						}
					}
				}
			}
		}
	}

	result := make([]DirectionGroup, 0, len(departuresByGroup))
	for key, departures := range departuresByGroup {
		sort.Slice(departures, func(i, j int) bool { return departures[i].Time.Before(departures[j].Time) })
		departures = dedupe(departures)
		if len(departures) > limit {
			departures = departures[:limit]
		}
		result = append(result, DirectionGroup{Route: f.Routes[key.route], Direction: int(key.direction),
			Headsign: departures[0].Headsign, Departures: departures})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Route.ShortName != result[j].Route.ShortName {
			return lessRouteName(result[i].Route.ShortName, result[j].Route.ShortName)
		}
		return result[i].Direction < result[j].Direction
	})
	return result
}

// dedupe drops repeats of the same trip at the same time, which happens when
// a station and one of its platforms both list it. deps must be sorted.
func dedupe(departures []Departure) []Departure {
	type tripRun struct {
		trip string
		at   int64
	}
	seen := map[tripRun]bool{}
	out := departures[:0]
	for _, departure := range departures {
		run := tripRun{departure.TripID, departure.Time.Unix()}
		if seen[run] {
			continue
		}
		seen[run] = true
		out = append(out, departure)
	}
	return out
}

// lessRouteName sorts "10" before "100" before "24" the way people expect:
// numerically when both are numbers, otherwise alphabetically.
func lessRouteName(a, b string) bool {
	if len(a) != len(b) && isDigits(a) && isDigits(b) {
		return len(a) < len(b)
	}
	return a < b
}

func isDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

// serviceDayStart is GTFS's reference point for schedule times: noon minus
// 12 hours on the service date. On DST change days this differs from
// midnight, which keeps 08:00:00 meaning 8 a.m. local time.
func serviceDayStart(day time.Time, timeZone *time.Location) time.Time {
	year, month, dayOfMonth := day.Date()
	return time.Date(year, month, dayOfMonth, 12, 0, 0, 0, timeZone).Add(-12 * time.Hour)
}

func (f *Feed) activeServices(day time.Time) map[string]bool {
	dateKey := day.Format("20060102")
	weekday := day.Weekday()
	active := map[string]bool{}
	for serviceID, calendar := range f.services {
		if calendar.days[weekday] && dateKey >= calendar.start && dateKey <= calendar.end {
			active[serviceID] = true
		}
	}
	for id := range f.servicesRemovedOn[dateKey] {
		delete(active, id)
	}
	for id := range f.servicesAddedOn[dateKey] {
		active[id] = true
	}
	return active
}

// SearchStops finds stops and stations whose name contains query, ignoring
// case and French accents ("cote" matches "Côte-des-Neiges"). Matching on a
// stop code ("52345") also works. Platforms of a station are left out in
// favour of the station itself.
func (f *Feed) SearchStops(query string, limit int) []*Stop {
	foldedQuery := foldAccents(query)
	var matches []*Stop
	for _, stop := range f.Stops {
		if stop.Parent != "" {
			continue
		}
		if stop.Code == query || strings.Contains(foldAccents(stop.Name), foldedQuery) {
			matches = append(matches, stop)
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].IsStation != matches[j].IsStation {
			return matches[i].IsStation
		}
		return matches[i].Name < matches[j].Name
	})
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
}

// NearbyStop is a stop with its distance from a search point.
type NearbyStop struct {
	*Stop
	Meters float64
}

// NearbyStops returns stops and stations within radius meters of a point,
// closest first. Metro platforms are folded into their station.
func (f *Feed) NearbyStops(lat, lon, radius float64, limit int) []NearbyStop {
	var nearby []NearbyStop
	for _, stop := range f.Stops {
		if stop.Parent != "" {
			continue
		}
		if meters := distance(lat, lon, stop.Lat, stop.Lon); meters <= radius {
			nearby = append(nearby, NearbyStop{stop, meters})
		}
	}
	sort.Slice(nearby, func(i, j int) bool { return nearby[i].Meters < nearby[j].Meters })
	if limit > 0 && len(nearby) > limit {
		nearby = nearby[:limit]
	}
	return nearby
}

// distance is the great-circle distance in meters.
func distance(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusMeters = 6371000
	degreesToRadians := math.Pi / 180
	dLat, dLon := (lat2-lat1)*degreesToRadians, (lon2-lon1)*degreesToRadians
	haversine := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*degreesToRadians)*math.Cos(lat2*degreesToRadians)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadiusMeters * math.Asin(math.Sqrt(haversine))
}

// RoutesAt lists the routes serving a stop or station, for the settings page.
func (f *Feed) RoutesAt(stopID string) []Route {
	routeIndexes := map[int32]bool{}
	for _, platformID := range append([]string{stopID}, f.platformsByStation[stopID]...) {
		for _, visit := range f.stopTimesByStop[platformID] {
			routeIndexes[f.trips[visit.tripIndex].routeIndex] = true
		}
	}
	routes := make([]Route, 0, len(routeIndexes))
	for i := range routeIndexes {
		routes = append(routes, f.Routes[i])
	}
	sort.Slice(routes, func(i, j int) bool { return lessRouteName(routes[i].ShortName, routes[j].ShortName) })
	return routes
}

var accentFolder = strings.NewReplacer(
	"à", "a", "â", "a", "ä", "a", "ç", "c", "é", "e", "è", "e", "ê", "e", "ë", "e",
	"î", "i", "ï", "i", "ô", "o", "ö", "o", "ù", "u", "û", "u", "ü", "u", "ÿ", "y",
	"œ", "oe", "æ", "ae",
)

func foldAccents(text string) string { return accentFolder.Replace(strings.ToLower(text)) }
