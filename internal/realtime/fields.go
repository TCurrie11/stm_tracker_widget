// stm_tracker_widget: live STM bus and métro departures for KDE Plasma 6
// https://github.com/TCurrie11/stm_tracker_widget
//
// File:    internal/realtime/fields.go
// Purpose: Named GTFS-Realtime protobuf field numbers.
// Author:  TCurrie11 (https://github.com/TCurrie11)
// Version: 0.1.0
// Created: 2026-10-14
// Updated: 2026-10-14
// License: GPL-2.0-or-later

package realtime

// Field numbers from gtfs-realtime.proto, named <message><field>, so the
// decoder reads like the spec rather than a list of magic numbers.
// https://github.com/google/transit/blob/master/gtfs-realtime/proto/gtfs-realtime.proto
const (
	feedMessageHeader = 1
	feedMessageEntity = 2

	feedHeaderVersion   = 1 // gtfs_realtime_version
	feedHeaderTimestamp = 3

	feedEntityIsDeleted  = 2
	feedEntityTripUpdate = 3
	feedEntityVehicle    = 4

	tripUpdateTrip           = 1
	tripUpdateStopTimeUpdate = 2
	tripUpdateTimestamp      = 4

	stopTimeUpdateStopSequence         = 1
	stopTimeUpdateArrival              = 2
	stopTimeUpdateDeparture            = 3
	stopTimeUpdateStopID               = 4
	stopTimeUpdateScheduleRelationship = 5

	stopTimeEventTime = 2

	tripDescriptorTripID               = 1
	tripDescriptorStartDate            = 3
	tripDescriptorScheduleRelationship = 4
	tripDescriptorRouteID              = 5
	tripDescriptorDirectionID          = 6

	vehiclePositionTrip                = 1
	vehiclePositionPosition            = 2
	vehiclePositionCurrentStopSequence = 3
	vehiclePositionCurrentStatus       = 4
	vehiclePositionTimestamp           = 5
	vehiclePositionStopID              = 7
	vehiclePositionVehicle             = 8
	vehiclePositionOccupancyStatus     = 9

	positionLatitude  = 1
	positionLongitude = 2
	positionBearing   = 3
	positionSpeed     = 5

	vehicleDescriptorID = 1
)

// TripDescriptor.ScheduleRelationship values that mean the trip won't run.
// (DUPLICATED = 6 is an extra run of a trip, not a cancellation.)
const (
	tripCanceled = 3
	tripDeleted  = 7
)
