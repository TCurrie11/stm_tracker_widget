// Package realtime decodes GTFS-Realtime feeds and matches them to the
// static schedule.
//
// Field numbers follow gtfs-realtime.proto:
// https://gtfs.org/documentation/realtime/proto/
package realtime

import (
	"fmt"
	"time"

	"stm-transit/internal/pb"
)

// StopRelationship is StopTimeUpdate.ScheduleRelationship.
type StopRelationship int

const (
	StopScheduled StopRelationship = 0
	StopSkipped   StopRelationship = 1 // detour: the bus won't serve this stop
	StopNoData    StopRelationship = 2 // no prediction; fall back to schedule
)

// StopUpdate is a prediction for one stop of a trip.
type StopUpdate struct {
	Sequence     uint32
	StopID       string
	Arrival      int64 // Unix seconds, 0 if absent
	Departure    int64 // Unix seconds, 0 if absent
	Relationship StopRelationship
}

// Time is the predicted departure, or arrival if only that is given.
func (s StopUpdate) Time() int64 {
	if s.Departure != 0 {
		return s.Departure
	}
	return s.Arrival
}

// TripUpdate holds predictions for one trip.
type TripUpdate struct {
	TripID    string
	StartDate string // YYYYMMDD service date
	RouteID   string
	Direction int
	Canceled  bool
	// Stops still ahead of the vehicle. Not necessarily in sequence order:
	// STM lists skipped stops after all the others.
	Stops     []StopUpdate
	Timestamp int64
}

// VehicleStatus is VehiclePosition.VehicleStopStatus.
type VehicleStatus int

const (
	IncomingAt  VehicleStatus = 0
	StoppedAt   VehicleStatus = 1
	InTransitTo VehicleStatus = 2 // the GTFS default when absent
)

// Occupancy is VehiclePosition.OccupancyStatus.
type Occupancy int

const (
	Empty                   Occupancy = 0
	ManySeatsAvailable      Occupancy = 1
	FewSeatsAvailable       Occupancy = 2
	StandingRoomOnly        Occupancy = 3
	CrushedStandingRoomOnly Occupancy = 4
	Full                    Occupancy = 5
	NotAcceptingPassengers  Occupancy = 6
	NoDataAvailable         Occupancy = 7
	NotBoardable            Occupancy = 8
	OccupancyUnknown        Occupancy = -1 // field absent
)

var occupancyNames = map[Occupancy]string{
	Empty: "EMPTY", ManySeatsAvailable: "MANY_SEATS_AVAILABLE",
	FewSeatsAvailable: "FEW_SEATS_AVAILABLE", StandingRoomOnly: "STANDING_ROOM_ONLY",
	CrushedStandingRoomOnly: "CRUSHED_STANDING_ROOM_ONLY", Full: "FULL",
	NotAcceptingPassengers: "NOT_ACCEPTING_PASSENGERS", NoDataAvailable: "NO_DATA_AVAILABLE",
	NotBoardable: "NOT_BOARDABLE",
}

func (o Occupancy) String() string {
	if s, ok := occupancyNames[o]; ok {
		return s
	}
	return ""
}

// Level maps occupancy onto a 0–3 scale for a simple bar:
// 0 unknown, 1 seats free, 2 few seats, 3 standing or full.
func (o Occupancy) Level() int {
	switch o {
	case Empty, ManySeatsAvailable:
		return 1
	case FewSeatsAvailable:
		return 2
	case StandingRoomOnly, CrushedStandingRoomOnly, Full, NotAcceptingPassengers:
		return 3
	}
	return 0
}

// VehiclePosition is where a vehicle is and how full it is.
type VehiclePosition struct {
	TripID     string
	StartDate  string
	RouteID    string
	VehicleID  string
	Lat, Lon   float32
	Bearing    float32
	Speed      float32 // m/s
	CurrentSeq uint32  // stop sequence the Status refers to; 0 if absent
	StopID     string
	Status     VehicleStatus
	Occupancy  Occupancy
	Timestamp  int64
}

// Feed is one decoded GTFS-Realtime message. Only one of Trips and
// Vehicles is normally filled, since STM publishes them as separate feeds.
type Feed struct {
	Timestamp int64
	Trips     []TripUpdate
	Vehicles  []VehiclePosition
}

// Decode parses a FeedMessage.
func Decode(b []byte) (*Feed, error) {
	f := &Feed{}
	r := pb.NewReader(b)
	for r.Next() {
		switch r.Field() {
		case 1: // header
			h := r.Message()
			var version string
			for h.Next() {
				switch h.Field() {
				case 1:
					version = h.String()
				case 3:
					f.Timestamp = int64(h.Uint64())
				}
			}
			if err := h.Err(); err != nil {
				return nil, fmt.Errorf("header: %w", err)
			}
			if version != "" && version[0] != '1' && version[0] != '2' {
				return nil, fmt.Errorf("unsupported gtfs-realtime version %q", version)
			}
		case 2: // entity
			if err := decodeEntity(r.Message(), f); err != nil {
				return nil, fmt.Errorf("entity %d: %w", len(f.Trips)+len(f.Vehicles), err)
			}
		}
	}
	if err := r.Err(); err != nil {
		return nil, err
	}
	return f, nil
}

func decodeEntity(r *pb.Reader, f *Feed) error {
	for r.Next() {
		switch r.Field() {
		case 2: // is_deleted (only meaningful for incremental feeds)
			if r.Bool() {
				return r.Err()
			}
		case 3:
			tu, err := decodeTripUpdate(r.Message())
			if err != nil {
				return err
			}
			f.Trips = append(f.Trips, tu)
		case 4:
			vp, err := decodeVehicle(r.Message())
			if err != nil {
				return err
			}
			f.Vehicles = append(f.Vehicles, vp)
		}
	}
	return r.Err()
}

// tripDescriptor fills the shared trip fields.
func tripDescriptor(r *pb.Reader) (tripID, startDate, routeID string, dir int, canceled bool, err error) {
	for r.Next() {
		switch r.Field() {
		case 1:
			tripID = r.String()
		case 3:
			startDate = r.String()
		case 4:
			// 3 = CANCELED; 6 = DELETED (newer spec) is treated the same.
			rel := r.Uint64()
			canceled = rel == 3 || rel == 6
		case 5:
			routeID = r.String()
		case 6:
			dir = int(r.Uint32())
		}
	}
	return tripID, startDate, routeID, dir, canceled, r.Err()
}

func decodeTripUpdate(r *pb.Reader) (TripUpdate, error) {
	var tu TripUpdate
	for r.Next() {
		switch r.Field() {
		case 1:
			var err error
			tu.TripID, tu.StartDate, tu.RouteID, tu.Direction, tu.Canceled, err = tripDescriptor(r.Message())
			if err != nil {
				return tu, err
			}
		case 2:
			su, err := decodeStopUpdate(r.Message())
			if err != nil {
				return tu, err
			}
			tu.Stops = append(tu.Stops, su)
		case 4:
			tu.Timestamp = int64(r.Uint64())
		}
	}
	return tu, r.Err()
}

func decodeStopUpdate(r *pb.Reader) (StopUpdate, error) {
	var su StopUpdate
	for r.Next() {
		switch r.Field() {
		case 1:
			su.Sequence = r.Uint32()
		case 2:
			su.Arrival = eventTime(r.Message())
		case 3:
			su.Departure = eventTime(r.Message())
		case 4:
			su.StopID = r.String()
		case 5:
			su.Relationship = StopRelationship(r.Uint64())
		}
	}
	return su, r.Err()
}

// eventTime reads StopTimeEvent.time. STM sends absolute times only; a
// feed that sends only delay would need the schedule to resolve it, which
// we don't support yet (the stop then falls back to schedule).
func eventTime(r *pb.Reader) int64 {
	var t int64
	for r.Next() {
		if r.Field() == 2 {
			t = r.Int64()
		}
	}
	return t
}

func decodeVehicle(r *pb.Reader) (VehiclePosition, error) {
	vp := VehiclePosition{Status: InTransitTo, Occupancy: OccupancyUnknown}
	for r.Next() {
		switch r.Field() {
		case 1:
			var err error
			vp.TripID, vp.StartDate, vp.RouteID, _, _, err = tripDescriptor(r.Message())
			if err != nil {
				return vp, err
			}
		case 2:
			p := r.Message()
			for p.Next() {
				switch p.Field() {
				case 1:
					vp.Lat = p.Float32()
				case 2:
					vp.Lon = p.Float32()
				case 3:
					vp.Bearing = p.Float32()
				case 5:
					vp.Speed = p.Float32()
				}
			}
			if err := p.Err(); err != nil {
				return vp, err
			}
		case 3:
			vp.CurrentSeq = r.Uint32()
		case 4:
			vp.Status = VehicleStatus(r.Uint64())
		case 5:
			vp.Timestamp = int64(r.Uint64())
		case 7:
			vp.StopID = r.String()
		case 8:
			d := r.Message()
			for d.Next() {
				if d.Field() == 1 {
					vp.VehicleID = d.String()
				}
			}
		case 9:
			vp.Occupancy = Occupancy(r.Uint64())
		}
	}
	return vp, r.Err()
}

// Time converts a feed timestamp.
func Time(unix int64) time.Time { return time.Unix(unix, 0) }
