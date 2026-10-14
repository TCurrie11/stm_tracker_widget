// stm_tracker_widget: live STM bus and métro departures for KDE Plasma 6
// https://github.com/TCurrie11/stm_tracker_widget
//
// File:    internal/poll/plan.go
// Purpose: Decides when live data is worth fetching.
// Author:  TCurrie11 (https://github.com/TCurrie11)
// Version: 0.1.0
// Created: 2026-10-14
// Updated: 2026-10-14
// License: GPL-2.0-or-later

// Package poll decides when the realtime feeds are worth fetching.
//
// Realtime data only matters when a bus or train on a route the user
// watches is about to leave. Overnight, after the last departure, polling
// stops and the widget keeps showing the next scheduled departure from the
// static timetable ("next at 5:30") without using any API requests.
package poll

import (
	"time"

	"github.com/TCurrie11/stm_tracker_widget/internal/gtfs"
)

// Watch is one stop and the routes the user follows there. An empty
// Routes list means every route at the stop.
type Watch struct {
	Stop   string
	Routes []string
}

// Plan says whether to poll now and, if not, when to look again.
type Plan struct {
	Active bool      // poll at the normal interval
	WakeAt time.Time // when Active is false: resume polling at this time
	Reason string    // for logs: "service", "no service until 05:30", ...
}

// Grace keeps polling for a while after a departure's scheduled time, so a
// late last bus still shows its live delay instead of vanishing.
const Grace = 20 * time.Minute

// recheck is how long to sleep when nothing departs within the schedule
// lookahead (very long gaps, or a feed past its end date).
const recheck = time.Hour

// Decide returns the plan for now. lead is how long before a departure
// polling should already be running, so predictions are warm (STM
// publishes trip updates before the bus reaches the first stop).
func Decide(feed *gtfs.Feed, watches []Watch, now time.Time, lead time.Duration) Plan {
	if len(watches) == 0 {
		return Plan{WakeAt: now.Add(recheck), Reason: "nothing watched"}
	}
	var earliest time.Time
	from := now.Add(-Grace)
	for _, watch := range watches {
		for _, group := range feed.Next(watch.Stop, from, 1, watch.Routes) {
			if departs := group.Departures[0].Time; earliest.IsZero() || departs.Before(earliest) {
				earliest = departs
			}
		}
	}
	if earliest.IsZero() {
		return Plan{WakeAt: now.Add(recheck), Reason: "no departures scheduled soon"}
	}
	wakeAt := earliest.Add(-lead)
	if !wakeAt.After(now) {
		return Plan{Active: true, Reason: "service"}
	}
	return Plan{WakeAt: wakeAt, Reason: "no service until " + earliest.In(feed.TimeZone).Format("15:04")}
}
