// stm_tracker_widget: live STM bus and métro departures for KDE Plasma 6
// https://github.com/TCurrie11/stm_tracker_widget
//
// File:    internal/realtime/match.go
// Purpose: Matches live predictions to scheduled departures.
// Author:  TCurrie11 (https://github.com/TCurrie11)
// Version: 0.1.0
// Created: 2026-10-14
// Updated: 2026-10-14
// License: GPL-2.0-or-later

package realtime

import (
	"time"

	"github.com/TCurrie11/stm_tracker_widget/internal/gtfs"
)

// Snapshot is the latest trip updates and vehicle positions, indexed for
// lookup by trip. Either feed may be missing.
type Snapshot struct {
	FeedTime      time.Time
	updatesByRun  map[tripRun]*TripUpdate
	vehiclesByRun map[tripRun]*VehiclePosition
}

// tripRun identifies one run of a trip: the same trip_id runs every day.
type tripRun struct{ tripID, serviceDate string }

// NewSnapshot indexes decoded feeds. Pass nil for a feed you don't have.
func NewSnapshot(tripUpdates, vehiclePositions *Feed) *Snapshot {
	snapshot := &Snapshot{updatesByRun: map[tripRun]*TripUpdate{}, vehiclesByRun: map[tripRun]*VehiclePosition{}}
	if tripUpdates != nil {
		snapshot.FeedTime = Time(tripUpdates.Timestamp)
		for i := range tripUpdates.Trips {
			update := &tripUpdates.Trips[i]
			snapshot.updatesByRun[tripRun{update.TripID, update.StartDate}] = update
		}
	}
	if vehiclePositions != nil {
		if positionsTime := Time(vehiclePositions.Timestamp); snapshot.FeedTime.IsZero() || positionsTime.Before(snapshot.FeedTime) {
			snapshot.FeedTime = positionsTime // report the older of the two
		}
		for i := range vehiclePositions.Vehicles {
			vehicle := &vehiclePositions.Vehicles[i]
			if vehicle.TripID != "" {
				snapshot.vehiclesByRun[tripRun{vehicle.TripID, vehicle.StartDate}] = vehicle
			}
		}
	}
	return snapshot
}

// Status is what the widget colours a departure by.
type Status string

const (
	Scheduled Status = "scheduled" // no live prediction: schedule time only
	OnTime    Status = "on_time"
	Late      Status = "late"
	Early     Status = "early"
	Canceled  Status = "canceled"
	Skipped   Status = "skipped" // detour: the bus won't stop here
)

// Thresholds decide when a delay counts as late or early.
type Thresholds struct {
	Late  time.Duration // at least this late → Late
	Early time.Duration // at least this early → Early
}

// DefaultThresholds: 2 minutes late, 1 minute early (early buses are the
// ones people miss, so the bar is lower).
var DefaultThresholds = Thresholds{Late: 2 * time.Minute, Early: time.Minute}

// Estimate is a scheduled departure combined with whatever live data
// exists for it.
type Estimate struct {
	Scheduled time.Time
	Expected  time.Time // = Scheduled unless Predicted
	Delay     time.Duration
	Status    Status
	Predicted bool // Expected comes from a live prediction
	Tracked   bool // a vehicle on this trip is reporting its position
	VehicleID string
	StopsAway int // stops until it reaches this one; 0 = at the stop, -1 = unknown
	Occupancy Occupancy
	Departed  bool // the vehicle has already passed this stop
}

// Estimate applies live data to one scheduled departure. A nil Snapshot
// is valid and yields schedule-only estimates.
func (s *Snapshot) Estimate(departure gtfs.Departure, thresholds Thresholds) Estimate {
	estimate := Estimate{Scheduled: departure.Time, Expected: departure.Time, Status: Scheduled,
		StopsAway: -1, Occupancy: OccupancyUnknown}
	if s == nil || departure.FromFrequency {
		return estimate // headway-based (metro) trips have no individual predictions
	}
	run := tripRun{departure.TripID, departure.ServiceDate}
	update, vehicle := s.updatesByRun[run], s.vehiclesByRun[run]

	if update != nil && update.Canceled {
		estimate.Status = Canceled
		return estimate
	}
	ourSequence := uint32(departure.Sequence)
	if vehicle != nil {
		estimate.Tracked, estimate.VehicleID, estimate.Occupancy = true, vehicle.VehicleID, vehicle.Occupancy
		if vehicle.CurrentSequence > ourSequence {
			estimate.Departed = true
			return estimate
		}
	}

	if update != nil {
		stopUpdate, found := findStop(update, ourSequence, departure.StopID)
		switch {
		case found && stopUpdate.Relationship == StopSkipped:
			estimate.Status = Skipped
			return estimate
		case found && stopUpdate.Relationship != StopNoData && stopUpdate.Time() != 0:
			estimate.Expected = Time(stopUpdate.Time())
			estimate.Predicted = true
		case !found && len(update.Stops) > 0 && minSequence(update) > ourSequence:
			// Predictions only cover stops still ahead of the bus, and ours
			// isn't among them: it has gone by.
			estimate.Departed = true
			return estimate
		}
	}

	if vehicle != nil {
		estimate.StopsAway = stopsAway(vehicle, update, ourSequence)
	}
	if estimate.Predicted {
		estimate.Delay = estimate.Expected.Sub(estimate.Scheduled)
		switch {
		case estimate.Delay >= thresholds.Late:
			estimate.Status = Late
		case -estimate.Delay >= thresholds.Early:
			estimate.Status = Early
		default:
			estimate.Status = OnTime
		}
	}
	return estimate
}

// minSequence is the earliest stop the trip still has predictions for.
// Don't use Stops[0]: STM appends skipped stops after the others, out of
// sequence order.
func minSequence(update *TripUpdate) uint32 {
	lowest := update.Stops[0].Sequence
	for _, stop := range update.Stops[1:] {
		if stop.Sequence < lowest {
			lowest = stop.Sequence
		}
	}
	return lowest
}

func findStop(update *TripUpdate, sequence uint32, stopID string) (StopUpdate, bool) {
	for _, stop := range update.Stops {
		if (stop.Sequence != 0 && stop.Sequence == sequence) || (stop.Sequence == 0 && stop.StopID == stopID) {
			return stop, true
		}
	}
	return StopUpdate{}, false
}

// stopsAway counts the stops the vehicle still has to serve before ours,
// plus ours: 1 means "next stop", 0 means it is at our stop now.
func stopsAway(vehicle *VehiclePosition, update *TripUpdate, ourSequence uint32) int {
	current := vehicle.CurrentSequence
	if current == 0 {
		return -1
	}
	// "Stopped at X" means X is behind it once it leaves; "in transit to X"
	// means X is still ahead.
	firstAhead := current
	if vehicle.Status == StoppedAt {
		firstAhead = current + 1
	}
	if update != nil && len(update.Stops) > 0 {
		// Count the stops the trip will actually serve, so a detour that
		// skips stops doesn't inflate the number.
		count := 0
		for _, stop := range update.Stops {
			if stop.Sequence >= firstAhead && stop.Sequence <= ourSequence && stop.Relationship != StopSkipped {
				count++
			}
		}
		return count
	}
	// No predictions to count: assume consecutive sequence numbers, which
	// is how STM numbers them.
	if ourSequence < firstAhead {
		return 0
	}
	return int(ourSequence-firstAhead) + 1
}
