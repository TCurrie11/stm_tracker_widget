// Package arrivals builds the departure board the widget shows: the next
// few departures per route and direction at a stop, with live data merged
// in. It is the one place the schedule and the realtime feeds meet.
package arrivals

import (
	"sort"
	"time"

	"github.com/TCurrie11/stm_tracker_widget/internal/gtfs"
	"github.com/TCurrie11/stm_tracker_widget/internal/realtime"
)

// Departure is one row on the board.
type Departure struct {
	realtime.Estimate
	TripID string
	// Frequency marks headway-based service (the metro): Expected is "a
	// train about every N minutes", not a particular vehicle.
	Frequency bool
}

// Group is one route in one direction.
type Group struct {
	Route      gtfs.Route
	Direction  int
	Headsign   string
	Departures []Departure
}

// lookback lets a bus whose scheduled time has passed still appear while
// it is running late.
const lookback = 30 * time.Minute

// Board returns up to n upcoming departures per route and direction.
// Canceled departures are listed but don't count toward n, so the board
// always shows n buses you can actually catch. snap may be nil.
func Board(f *gtfs.Feed, snap *realtime.Snapshot, stop string, routes []string,
	now time.Time, n int, th realtime.Thresholds) []Group {

	// Ask the schedule for more than n per group: some will turn out to be
	// gone (departed early) or canceled once live data is applied.
	sched := f.Next(stop, now.Add(-lookback), n*3+6, routes)
	out := make([]Group, 0, len(sched))
	for _, g := range sched {
		var deps []Departure
		for _, d := range g.Departures {
			e := snap.Estimate(d, th)
			if e.Departed || e.Expected.Before(now) {
				continue
			}
			deps = append(deps, Departure{Estimate: e, TripID: d.TripID, Frequency: d.FromFrequency})
		}
		// A late bus can now be due after the next one; show them in the
		// order they'll actually arrive.
		sort.SliceStable(deps, func(i, j int) bool { return deps[i].Expected.Before(deps[j].Expected) })
		kept, catchable := deps[:0], 0
		for _, d := range deps {
			if catchable == n {
				break
			}
			if d.Status != realtime.Canceled && d.Status != realtime.Skipped {
				catchable++
			}
			kept = append(kept, d)
		}
		if len(kept) == 0 {
			continue
		}
		out = append(out, Group{Route: g.Route, Direction: g.Direction, Headsign: g.Headsign, Departures: kept})
	}
	return out
}
