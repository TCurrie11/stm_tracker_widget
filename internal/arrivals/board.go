// stm_tracker_widget: live STM bus and métro departures for KDE Plasma 6
// https://github.com/TCurrie11/stm_tracker_widget
//
// File:    internal/arrivals/board.go
// Purpose: Builds the departure board by merging schedule and live data.
// Author:  TCurrie11 (https://github.com/TCurrie11)
// Version: 0.1.0
// Created: 2026-10-14
// Updated: 2026-10-14
// License: GPL-2.0-or-later

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

// Board returns up to perDirection upcoming departures per route and
// direction. Canceled departures are listed but don't count toward that
// limit, so the board always shows perDirection buses you can actually
// catch. live may be nil (schedule only).
func Board(feed *gtfs.Feed, live *realtime.Snapshot, stopID string, routeFilter []string,
	now time.Time, perDirection int, thresholds realtime.Thresholds) []Group {

	// Ask the schedule for more than perDirection per group: some will turn out to be
	// gone (departed early) or canceled once live data is applied.
	scheduled := feed.Next(stopID, now.Add(-lookback), perDirection*3+6, routeFilter)
	board := make([]Group, 0, len(scheduled))
	for _, group := range scheduled {
		var departures []Departure
		for _, planned := range group.Departures {
			estimate := live.Estimate(planned, thresholds)
			if estimate.Departed || estimate.Expected.Before(now) {
				continue
			}
			departures = append(departures, Departure{Estimate: estimate, TripID: planned.TripID, Frequency: planned.FromFrequency})
		}
		// A late bus can now be due after the next one; show them in the
		// order they'll actually arrive.
		sort.SliceStable(departures, func(i, j int) bool { return departures[i].Expected.Before(departures[j].Expected) })
		kept, catchable := departures[:0], 0
		for _, departure := range departures {
			if catchable == perDirection {
				break
			}
			if departure.Status != realtime.Canceled && departure.Status != realtime.Skipped {
				catchable++
			}
			kept = append(kept, departure)
		}
		if len(kept) == 0 {
			continue
		}
		board = append(board, Group{Route: group.Route, Direction: group.Direction, Headsign: group.Headsign, Departures: kept})
	}
	return board
}
