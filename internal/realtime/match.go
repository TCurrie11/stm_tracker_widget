package realtime

import (
	"time"

	"stm-transit/internal/gtfs"
)

// Snapshot is the latest trip updates and vehicle positions, indexed for
// lookup by trip. Either feed may be missing.
type Snapshot struct {
	FeedTime time.Time
	trips    map[tripKey]*TripUpdate
	vehicles map[tripKey]*VehiclePosition
}

// tripKey identifies one run of a trip: the same trip_id runs every day.
type tripKey struct{ trip, date string }

// NewSnapshot indexes decoded feeds. Pass nil for a feed you don't have.
func NewSnapshot(tripUpdates, vehiclePositions *Feed) *Snapshot {
	s := &Snapshot{trips: map[tripKey]*TripUpdate{}, vehicles: map[tripKey]*VehiclePosition{}}
	if tripUpdates != nil {
		s.FeedTime = Time(tripUpdates.Timestamp)
		for i := range tripUpdates.Trips {
			tu := &tripUpdates.Trips[i]
			s.trips[tripKey{tu.TripID, tu.StartDate}] = tu
		}
	}
	if vehiclePositions != nil {
		if t := Time(vehiclePositions.Timestamp); s.FeedTime.IsZero() || t.Before(s.FeedTime) {
			s.FeedTime = t // report the older of the two
		}
		for i := range vehiclePositions.Vehicles {
			vp := &vehiclePositions.Vehicles[i]
			if vp.TripID != "" {
				s.vehicles[tripKey{vp.TripID, vp.StartDate}] = vp
			}
		}
	}
	return s
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
func (s *Snapshot) Estimate(d gtfs.Departure, th Thresholds) Estimate {
	e := Estimate{Scheduled: d.Time, Expected: d.Time, Status: Scheduled,
		StopsAway: -1, Occupancy: OccupancyUnknown}
	if s == nil || d.FromFrequency {
		return e // headway-based (metro) trips have no individual predictions
	}
	key := tripKey{d.TripID, d.ServiceDate}
	tu, vp := s.trips[key], s.vehicles[key]

	if tu != nil && tu.Canceled {
		e.Status = Canceled
		return e
	}
	ours := uint32(d.Sequence)
	if vp != nil {
		e.Tracked, e.VehicleID, e.Occupancy = true, vp.VehicleID, vp.Occupancy
		if vp.CurrentSeq > ours {
			e.Departed = true
			return e
		}
	}

	if tu != nil {
		su, found := findStop(tu, ours, d.StopID)
		switch {
		case found && su.Relationship == StopSkipped:
			e.Status = Skipped
			return e
		case found && su.Relationship != StopNoData && su.Time() != 0:
			e.Expected = Time(su.Time())
			e.Predicted = true
		case !found && len(tu.Stops) > 0 && minSequence(tu) > ours:
			// Predictions only cover stops still ahead of the bus, and ours
			// isn't among them: it has gone by.
			e.Departed = true
			return e
		}
	}

	if vp != nil {
		e.StopsAway = stopsAway(vp, tu, ours)
	}
	if e.Predicted {
		e.Delay = e.Expected.Sub(e.Scheduled)
		switch {
		case e.Delay >= th.Late:
			e.Status = Late
		case -e.Delay >= th.Early:
			e.Status = Early
		default:
			e.Status = OnTime
		}
	}
	return e
}

// minSequence is the earliest stop the trip still has predictions for.
// Don't use Stops[0]: STM appends skipped stops after the others, out of
// sequence order.
func minSequence(tu *TripUpdate) uint32 {
	m := tu.Stops[0].Sequence
	for _, s := range tu.Stops[1:] {
		if s.Sequence < m {
			m = s.Sequence
		}
	}
	return m
}

func findStop(tu *TripUpdate, seq uint32, stopID string) (StopUpdate, bool) {
	for _, s := range tu.Stops {
		if (s.Sequence != 0 && s.Sequence == seq) || (s.Sequence == 0 && s.StopID == stopID) {
			return s, true
		}
	}
	return StopUpdate{}, false
}

// stopsAway counts the stops the vehicle still has to serve before ours,
// plus ours: 1 means "next stop", 0 means it is at our stop now.
func stopsAway(vp *VehiclePosition, tu *TripUpdate, ours uint32) int {
	cur := vp.CurrentSeq
	if cur == 0 {
		return -1
	}
	// "Stopped at X" means X is behind it once it leaves; "in transit to X"
	// means X is still ahead.
	from := cur
	if vp.Status == StoppedAt {
		from = cur + 1
	}
	if tu != nil && len(tu.Stops) > 0 {
		// Count the stops the trip will actually serve, so a detour that
		// skips stops doesn't inflate the number.
		n := 0
		for _, s := range tu.Stops {
			if s.Sequence >= from && s.Sequence <= ours && s.Relationship != StopSkipped {
				n++
			}
		}
		return n
	}
	// No predictions to count: assume consecutive sequence numbers, which
	// is how STM numbers them.
	if ours < from {
		return 0
	}
	return int(ours-from) + 1
}
