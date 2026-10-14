// stm_tracker_widget: live STM bus and métro departures for KDE Plasma 6
// https://github.com/TCurrie11/stm_tracker_widget
//
// File:    internal/realtime/feed.go
// Purpose: Decodes GTFS-Realtime trip updates and vehicle positions.
// Author:  TCurrie11 (https://github.com/TCurrie11)
// Version: 0.1.0
// Created: 2026-10-14
// Updated: 2026-10-14
// License: GPL-2.0-or-later

// Package realtime decodes GTFS-Realtime feeds and matches them to the
// static schedule.
//
// Field numbers follow gtfs-realtime.proto:
// https://gtfs.org/documentation/realtime/proto/
package realtime

import (
	"fmt"
	"time"

	"github.com/TCurrie11/stm_tracker_widget/internal/pb"
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
	TripID          string
	StartDate       string
	RouteID         string
	VehicleID       string
	Lat, Lon        float32
	Bearing         float32
	Speed           float32 // m/s
	CurrentSequence uint32  // stop sequence the Status refers to; 0 if absent
	StopID          string
	Status          VehicleStatus
	Occupancy       Occupancy
	Timestamp       int64
}

// Feed is one decoded GTFS-Realtime message. Only one of Trips and
// Vehicles is normally filled, since STM publishes them as separate feeds.
type Feed struct {
	Timestamp int64
	Trips     []TripUpdate
	Vehicles  []VehiclePosition
}

// Decode parses a FeedMessage.
func Decode(message []byte) (*Feed, error) {
	feed := &Feed{}
	reader := pb.NewReader(message)
	for reader.Next() {
		switch reader.Field() {
		case feedMessageHeader:
			header := reader.Message()
			var version string
			for header.Next() {
				switch header.Field() {
				case feedHeaderVersion:
					version = header.String()
				case feedHeaderTimestamp:
					feed.Timestamp = int64(header.Uint64())
				}
			}
			if err := header.Err(); err != nil {
				return nil, fmt.Errorf("header: %w", err)
			}
			if version != "" && version[0] != '1' && version[0] != '2' {
				return nil, fmt.Errorf("unsupported gtfs-realtime version %q", version)
			}
		case feedMessageEntity:
			if err := decodeEntity(reader.Message(), feed); err != nil {
				return nil, fmt.Errorf("entity %d: %w", len(feed.Trips)+len(feed.Vehicles), err)
			}
		}
	}
	if err := reader.Err(); err != nil {
		return nil, err
	}
	return feed, nil
}

func decodeEntity(entity *pb.Reader, feed *Feed) error {
	for entity.Next() {
		switch entity.Field() {
		case feedEntityIsDeleted: // only meaningful for incremental feeds
			if entity.Bool() {
				return entity.Err()
			}
		case feedEntityTripUpdate:
			tripUpdate, err := decodeTripUpdate(entity.Message())
			if err != nil {
				return err
			}
			feed.Trips = append(feed.Trips, tripUpdate)
		case feedEntityVehicle:
			vehicle, err := decodeVehicle(entity.Message())
			if err != nil {
				return err
			}
			feed.Vehicles = append(feed.Vehicles, vehicle)
		}
	}
	return entity.Err()
}

// tripDescriptor fills the shared trip fields.
func tripDescriptor(descriptor *pb.Reader) (tripID, startDate, routeID string, direction int, canceled bool, err error) {
	for descriptor.Next() {
		switch descriptor.Field() {
		case tripDescriptorTripID:
			tripID = descriptor.String()
		case tripDescriptorStartDate:
			startDate = descriptor.String()
		case tripDescriptorScheduleRelationship:
			// DELETED (newer spec) is treated the same as CANCELED.
			relationship := descriptor.Uint64()
			canceled = relationship == tripCanceled || relationship == tripDeleted
		case tripDescriptorRouteID:
			routeID = descriptor.String()
		case tripDescriptorDirectionID:
			direction = int(descriptor.Uint32())
		}
	}
	return tripID, startDate, routeID, direction, canceled, descriptor.Err()
}

func decodeTripUpdate(message *pb.Reader) (TripUpdate, error) {
	var update TripUpdate
	for message.Next() {
		switch message.Field() {
		case tripUpdateTrip:
			var err error
			update.TripID, update.StartDate, update.RouteID, update.Direction, update.Canceled, err = tripDescriptor(message.Message())
			if err != nil {
				return update, err
			}
		case tripUpdateStopTimeUpdate:
			stopUpdate, err := decodeStopUpdate(message.Message())
			if err != nil {
				return update, err
			}
			update.Stops = append(update.Stops, stopUpdate)
		case tripUpdateTimestamp:
			update.Timestamp = int64(message.Uint64())
		}
	}
	return update, message.Err()
}

func decodeStopUpdate(message *pb.Reader) (StopUpdate, error) {
	var update StopUpdate
	for message.Next() {
		switch message.Field() {
		case stopTimeUpdateStopSequence:
			update.Sequence = message.Uint32()
		case stopTimeUpdateArrival:
			update.Arrival = eventTime(message.Message())
		case stopTimeUpdateDeparture:
			update.Departure = eventTime(message.Message())
		case stopTimeUpdateStopID:
			update.StopID = message.String()
		case stopTimeUpdateScheduleRelationship:
			update.Relationship = StopRelationship(message.Uint64())
		}
	}
	return update, message.Err()
}

// eventTime reads StopTimeEvent.time. STM sends absolute times only; a
// feed that sends only delay would need the schedule to resolve it, which
// we don't support yet (the stop then falls back to schedule).
func eventTime(message *pb.Reader) int64 {
	var unixTime int64
	for message.Next() {
		if message.Field() == stopTimeEventTime {
			unixTime = message.Int64()
		}
	}
	return unixTime
}

func decodeVehicle(message *pb.Reader) (VehiclePosition, error) {
	vehicle := VehiclePosition{Status: InTransitTo, Occupancy: OccupancyUnknown}
	for message.Next() {
		switch message.Field() {
		case vehiclePositionTrip:
			var err error
			vehicle.TripID, vehicle.StartDate, vehicle.RouteID, _, _, err = tripDescriptor(message.Message())
			if err != nil {
				return vehicle, err
			}
		case vehiclePositionPosition:
			position := message.Message()
			for position.Next() {
				switch position.Field() {
				case positionLatitude:
					vehicle.Lat = position.Float32()
				case positionLongitude:
					vehicle.Lon = position.Float32()
				case positionBearing:
					vehicle.Bearing = position.Float32()
				case positionSpeed:
					vehicle.Speed = position.Float32()
				}
			}
			if err := position.Err(); err != nil {
				return vehicle, err
			}
		case vehiclePositionCurrentStopSequence:
			vehicle.CurrentSequence = message.Uint32()
		case vehiclePositionCurrentStatus:
			vehicle.Status = VehicleStatus(message.Uint64())
		case vehiclePositionTimestamp:
			vehicle.Timestamp = int64(message.Uint64())
		case vehiclePositionStopID:
			vehicle.StopID = message.String()
		case vehiclePositionVehicle:
			descriptor := message.Message()
			for descriptor.Next() {
				if descriptor.Field() == vehicleDescriptorID {
					vehicle.VehicleID = descriptor.String()
				}
			}
		case vehiclePositionOccupancyStatus:
			vehicle.Occupancy = Occupancy(message.Uint64())
		}
	}
	return vehicle, message.Err()
}

// Time converts a feed timestamp.
func Time(unix int64) time.Time { return time.Unix(unix, 0) }
