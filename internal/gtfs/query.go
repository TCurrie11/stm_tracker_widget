package gtfs

import (
	"sort"
	"strings"
	"time"
)

// Departure is one scheduled departure from a stop.
type Departure struct {
	Time     time.Time
	TripID   string // used later to match GTFS-Realtime updates
	Headsign string
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

// Next returns, for each route and direction serving stopID, the next n
// departures at or after now. stopID may be a metro station, in which case
// all of its platforms are included. If routes is non-empty, only those
// route IDs (or short names) are returned.
func (f *Feed) Next(stopID string, now time.Time, n int, routes []string) []DirectionGroup {
	now = now.In(f.Loc)
	stopIDs := append([]string{stopID}, f.children[stopID]...)
	want := map[int32]bool{}
	for _, r := range routes {
		if i, ok := f.routeByID[r]; ok {
			want[i] = true
			continue
		}
		for i, rt := range f.Routes {
			if rt.ShortName == r {
				want[int32(i)] = true
			}
		}
	}

	type key struct {
		route int32
		dir   int8
		head  string // only used when direction is missing
	}
	groups := map[key][]Departure{}
	until := now.Add(horizon)

	// A departure at 25:30 on yesterday's service is 01:30 today, so check
	// yesterday; tomorrow covers late-evening queries.
	for d := -1; d <= 1; d++ {
		date := now.AddDate(0, 0, d)
		active := f.activeServices(date)
		base := serviceDayStart(date, f.Loc)
		for _, sid := range stopIDs {
			for _, st := range f.byStop[sid] {
				tr := f.trips[st.tripIdx]
				if !active[tr.serviceID] || (len(routes) > 0 && !want[tr.routeIdx]) {
					continue
				}
				k := key{route: tr.routeIdx, dir: tr.direction}
				if tr.direction < 0 {
					k.head = tr.headsign
				}
				add := func(secs int32, freq bool) bool {
					t := base.Add(time.Duration(secs) * time.Second)
					if t.Before(now) {
						return true
					}
					if t.After(until) {
						return false
					}
					groups[k] = append(groups[k], Departure{Time: t, TripID: f.tripIDs[st.tripIdx],
						Headsign: tr.headsign, FromFrequency: freq})
					return true
				}
				fr, isFreq := f.freqs[st.tripIdx]
				if !isFreq {
					add(st.departure, false)
					continue
				}
				// Expand headway-based trips: offset of this stop within the trip,
				// repeated every headway across each window. Only the first n
				// future occurrences per window can matter.
				offset := st.departure - f.tripStart[st.tripIdx]
				nowSecs := int32(now.Sub(base).Seconds()) - offset
				for _, w := range fr {
					start := w.start
					if nowSecs > start {
						skip := (nowSecs - start + w.headway - 1) / w.headway
						start += skip * w.headway
					}
					for i, t := 0, start; i < n && t < w.end; i, t = i+1, t+w.headway {
						if !add(t+offset, true) {
							break
						}
					}
				}
			}
		}
	}

	out := make([]DirectionGroup, 0, len(groups))
	for k, deps := range groups {
		sort.Slice(deps, func(i, j int) bool { return deps[i].Time.Before(deps[j].Time) })
		deps = dedupe(deps)
		if len(deps) > n {
			deps = deps[:n]
		}
		out = append(out, DirectionGroup{Route: f.Routes[k.route], Direction: int(k.dir),
			Headsign: deps[0].Headsign, Departures: deps})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Route.ShortName != out[j].Route.ShortName {
			return lessRouteName(out[i].Route.ShortName, out[j].Route.ShortName)
		}
		return out[i].Direction < out[j].Direction
	})
	return out
}

// dedupe drops repeats of the same trip at the same time, which happens when
// a station and one of its platforms both list it. deps must be sorted.
func dedupe(deps []Departure) []Departure {
	type k struct {
		trip string
		at   int64
	}
	seen := map[k]bool{}
	out := deps[:0]
	for _, d := range deps {
		key := k{d.TripID, d.Time.Unix()}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, d)
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
func serviceDayStart(d time.Time, loc *time.Location) time.Time {
	y, m, day := d.Date()
	return time.Date(y, m, day, 12, 0, 0, 0, loc).Add(-12 * time.Hour)
}

func (f *Feed) activeServices(d time.Time) map[string]bool {
	date := d.Format("20060102")
	wd := d.Weekday()
	active := map[string]bool{}
	for id, s := range f.services {
		if s.days[wd] && date >= s.start && date <= s.end {
			active[id] = true
		}
	}
	for id := range f.removed[date] {
		delete(active, id)
	}
	for id := range f.added[date] {
		active[id] = true
	}
	return active
}

// SearchStops finds stops and stations whose name contains query, ignoring
// case and French accents ("cote" matches "Côte-des-Neiges"). Matching on a
// stop code ("52345") also works. Platforms of a station are left out in
// favour of the station itself.
func (f *Feed) SearchStops(query string, limit int) []*Stop {
	q := fold(query)
	var out []*Stop
	for _, s := range f.Stops {
		if s.Parent != "" {
			continue
		}
		if s.Code == query || strings.Contains(fold(s.Name), q) {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsStation != out[j].IsStation {
			return out[i].IsStation
		}
		return out[i].Name < out[j].Name
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

var accentFolder = strings.NewReplacer(
	"à", "a", "â", "a", "ä", "a", "ç", "c", "é", "e", "è", "e", "ê", "e", "ë", "e",
	"î", "i", "ï", "i", "ô", "o", "ö", "o", "ù", "u", "û", "u", "ü", "u", "ÿ", "y",
	"œ", "oe", "æ", "ae",
)

func fold(s string) string { return accentFolder.Replace(strings.ToLower(s)) }
