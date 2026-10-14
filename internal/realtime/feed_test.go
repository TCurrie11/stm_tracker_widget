// stm_tracker_widget: live STM bus and métro departures for KDE Plasma 6
// https://github.com/TCurrie11/stm_tracker_widget
//
// File:    internal/realtime/feed_test.go
// Purpose: Tests for GTFS-Realtime decoding against captured STM feeds.
// Author:  TCurrie11 (https://github.com/TCurrie11)
// Version: 0.1.0
// Created: 2026-10-14
// Updated: 2026-10-14
// License: GPL-2.0-or-later

package realtime

import (
	"github.com/TCurrie11/stm_tracker_widget/internal/pb"

	"compress/gzip"
	"io"
	"os"
	"testing"
)

// The testdata files are real STM feeds captured on 2026-10-14 at 11:23
// (Montréal time), so these numbers are facts about that snapshot.

func readGz(t *testing.T, name string) []byte {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDecodeSTMTripUpdates(t *testing.T) {
	f, err := Decode(readGz(t, "tripupdates.pb.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if f.Timestamp != 1791991395 {
		t.Errorf("timestamp %d", f.Timestamp)
	}
	if len(f.Trips) != 1682 || len(f.Vehicles) != 0 {
		t.Fatalf("got %d trips, %d vehicles", len(f.Trips), len(f.Vehicles))
	}
	var canceled, stops, skipped, noData int
	for _, tu := range f.Trips {
		if tu.Canceled {
			canceled++
		}
		for _, s := range tu.Stops {
			stops++
			switch s.Relationship {
			case StopSkipped:
				skipped++
			case StopNoData:
				noData++
			default:
				if s.Time() == 0 {
					t.Fatalf("trip %s stop %d: scheduled stop without a time", tu.TripID, s.Sequence)
				}
			}
		}
	}
	if canceled != 86 || stops != 47079 || skipped != 1212 || noData != 68 {
		t.Errorf("canceled=%d stops=%d skipped=%d noData=%d", canceled, stops, skipped, noData)
	}
	first := f.Trips[0]
	if first.TripID != "301170575" || first.StartDate != "20261014" || first.RouteID != "179" ||
		first.Stops[0].Sequence != 15 || first.Stops[0].StopID != "50698" || first.Stops[0].Departure != 1791991464 {
		t.Errorf("first trip %+v", first.Stops[0])
	}
}

func TestDecodeSTMVehiclePositions(t *testing.T) {
	f, err := Decode(readGz(t, "vehiclepositions.pb.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Vehicles) != 578 {
		t.Fatalf("got %d vehicles", len(f.Vehicles))
	}
	occ := map[Occupancy]int{}
	status := map[VehicleStatus]int{}
	for _, v := range f.Vehicles {
		occ[v.Occupancy]++
		status[v.Status]++
		if v.Lat < 45 || v.Lat > 46 || v.Lon < -74.5 || v.Lon > -73 {
			t.Errorf("vehicle %s outside Montréal: %f,%f", v.VehicleID, v.Lat, v.Lon)
		}
	}
	if occ[ManySeatsAvailable] != 307 || occ[FewSeatsAvailable] != 198 || occ[StandingRoomOnly] != 73 {
		t.Errorf("occupancy %v", occ)
	}
	if status[StoppedAt] != 189 || status[InTransitTo] != 389 {
		t.Errorf("status %v", status)
	}
	v := f.Vehicles[0]
	if v.VehicleID != "40068" || v.TripID != "301713392" || v.RouteID != "496" ||
		v.CurrentSequence != 22 || v.StopID != "61330" || v.Occupancy != StandingRoomOnly {
		t.Errorf("first vehicle %+v", v)
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	for _, b := range [][]byte{
		{0x12, 0x05, 0x01},       // entity claims 5 bytes, has 1
		{0x0a},                   // header tag with no length
		{0x13},                   // wire type 3 (group)
		{0x12, 0x02, 0x1a, 0x05}, // trip_update truncated inside entity
	} {
		if _, err := Decode(b); err == nil {
			t.Errorf("% x: expected an error", b)
		}
	}
	if f, err := Decode(nil); err != nil || len(f.Trips) != 0 {
		t.Errorf("empty message should decode to an empty feed: %v", err)
	}
}

func TestOccupancyLevel(t *testing.T) {
	for o, want := range map[Occupancy]int{
		OccupancyUnknown: 0, NoDataAvailable: 0, ManySeatsAvailable: 1,
		FewSeatsAvailable: 2, StandingRoomOnly: 3, Full: 3,
	} {
		if got := o.Level(); got != want {
			t.Errorf("%v: level %d, want %d", o, got, want)
		}
	}
}

// A DUPLICATED trip (6) is an extra run, not a cancellation; CANCELED (3)
// and DELETED (7) both mean it won't run.
func TestTripDescriptorScheduleRelationship(t *testing.T) {
	for relationship, wantCanceled := range map[byte]bool{0: false, 3: true, 6: false, 7: true} {
		// field 4 (schedule_relationship), wire type varint
		message := []byte{tripDescriptorScheduleRelationship<<3 | 0, relationship}
		_, _, _, _, canceled, err := tripDescriptor(pb.NewReader(message))
		if err != nil || canceled != wantCanceled {
			t.Errorf("relationship %d: canceled=%v err=%v, want canceled=%v", relationship, canceled, err, wantCanceled)
		}
	}
}
