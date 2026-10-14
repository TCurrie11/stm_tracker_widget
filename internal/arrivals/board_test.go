// stm_tracker_widget: live STM bus and métro departures for KDE Plasma 6
// https://github.com/TCurrie11/stm_tracker_widget
//
// File:    internal/arrivals/board_test.go
// Purpose: Tests for the departure board.
// Author:  TCurrie11 (https://github.com/TCurrie11)
// Version: 0.1.0
// Created: 2026-10-14
// Updated: 2026-10-14
// License: GPL-2.0-or-later

package arrivals

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/TCurrie11/stm_tracker_widget/internal/gtfs"
	"github.com/TCurrie11/stm_tracker_widget/internal/gtfs/gtfstest"
	"github.com/TCurrie11/stm_tracker_widget/internal/realtime"
)

// The fixture's route 24 Est leaves stop 52345 (stop_sequence 10) at
// 08:00, 08:15, 08:30, 08:45 on weekdays. Monday 2026-10-12 is a weekday.

func feed(t *testing.T) *gtfs.Feed {
	t.Helper()
	loc, _ := time.LoadLocation("America/Montreal")
	f, err := gtfs.LoadBytes(gtfstest.Zip(), loc)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func at(f *gtfs.Feed, hhmm string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", "2026-10-12 "+hhmm, f.TimeZone)
	if err != nil {
		panic(err)
	}
	return t
}

func stop(seq uint32, t time.Time) realtime.StopUpdate {
	return realtime.StopUpdate{Sequence: seq, Departure: t.Unix()}
}

func trip(id string, stops ...realtime.StopUpdate) realtime.TripUpdate {
	return realtime.TripUpdate{TripID: id, StartDate: "20261012", RouteID: "24", Stops: stops}
}

// row renders a departure compactly for comparison.
func row(d Departure) string {
	s := fmt.Sprintf("%s %s %s", d.TripID, d.Expected.Format("15:04"), d.Status)
	if d.StopsAway >= 0 {
		s += fmt.Sprintf(" %dstops", d.StopsAway)
	}
	if d.Occupancy != realtime.OccupancyUnknown {
		s += " occ" + fmt.Sprint(d.Occupancy.Level())
	}
	return s
}

func rows(g Group) string {
	var out []string
	for _, d := range g.Departures {
		out = append(out, row(d))
	}
	return strings.Join(out, " | ")
}

func TestBoardMergesLiveData(t *testing.T) {
	f := feed(t)
	skipped := realtime.StopUpdate{Sequence: 9, Relationship: realtime.StopSkipped}
	tu := &realtime.Feed{Trips: []realtime.TripUpdate{
		// 08:00 running 4 minutes late, a detour skips stop 9 before ours.
		trip("24E-0800", stop(8, at(f, "08:02:00")), skipped, stop(10, at(f, "08:04:10"))),
		// 08:15 canceled.
		{TripID: "24E-0815", StartDate: "20261012", Canceled: true},
		// 08:30 one minute early.
		trip("24E-0830", stop(10, at(f, "08:29:00"))),
		// 08:45 has no live data at all.
	}}
	vp := &realtime.Feed{Vehicles: []realtime.VehiclePosition{
		{TripID: "24E-0800", StartDate: "20261012", VehicleID: "40068", CurrentSequence: 8,
			Status: realtime.InTransitTo, Occupancy: realtime.FewSeatsAvailable},
	}}
	snap := realtime.NewSnapshot(tu, vp)

	got := Board(f, snap, "52345", []string{"24"}, at(f, "08:01:00"), 3, realtime.DefaultThresholds)
	if len(got) != 1 {
		t.Fatalf("want 1 group, got %d", len(got))
	}
	// The late 08:00 still shows (it hasn't come yet); the canceled 08:15 is
	// listed but doesn't use one of the three slots; stops away skips the
	// detoured stop 9.
	want := "24E-0800 08:04 late 2stops occ2 | 24E-0815 08:15 canceled | 24E-0830 08:29 early | 24E-0845 08:45 scheduled"
	if r := rows(got[0]); r != want {
		t.Errorf("\n got: %s\nwant: %s", r, want)
	}
	if d := got[0].Departures[0].Delay; d != 4*time.Minute+10*time.Second {
		t.Errorf("delay %v", d)
	}
}

func TestBoardReordersByExpectedTime(t *testing.T) {
	f := feed(t)
	// 08:15 is so late (08:33) that 08:30 (on time) arrives first.
	tu := &realtime.Feed{Trips: []realtime.TripUpdate{
		trip("24E-0815", stop(10, at(f, "08:33:00"))),
		trip("24E-0830", stop(10, at(f, "08:30:30"))),
	}}
	got := Board(f, realtime.NewSnapshot(tu, nil), "52345", nil, at(f, "08:10:00"), 2, realtime.DefaultThresholds)
	want := "24E-0830 08:30 on_time | 24E-0815 08:33 late"
	if r := rows(got[0]); r != want {
		t.Errorf("\n got: %s\nwant: %s", r, want)
	}
}

func TestBoardDropsBusesThatHavePassed(t *testing.T) {
	f := feed(t)
	tu := &realtime.Feed{Trips: []realtime.TripUpdate{
		// Predictions start after our stop (seq 10): it has gone by, even
		// though its scheduled time (08:15) is still in the future.
		trip("24E-0815", stop(11, at(f, "08:13:00"))),
	}}
	vp := &realtime.Feed{Vehicles: []realtime.VehiclePosition{
		// Vehicle position says the same for 08:30.
		{TripID: "24E-0830", StartDate: "20261012", CurrentSequence: 12},
	}}
	got := Board(f, realtime.NewSnapshot(tu, vp), "52345", nil, at(f, "08:12:00"), 1, realtime.DefaultThresholds)
	if r := rows(got[0]); r != "24E-0845 08:45 scheduled" {
		t.Errorf("got %s", r)
	}
}

func TestSkippedStopsListedLast(t *testing.T) {
	// STM's real ordering: served stops in sequence, then skipped ones.
	f := feed(t)
	tu := &realtime.Feed{Trips: []realtime.TripUpdate{
		trip("24E-0830", stop(7, at(f, "08:25:00")), stop(9, at(f, "08:27:00")),
			stop(10, at(f, "08:31:00")), stop(11, at(f, "08:32:00")),
			realtime.StopUpdate{Sequence: 8, Relationship: realtime.StopSkipped},
			realtime.StopUpdate{Sequence: 6, Relationship: realtime.StopSkipped}),
	}}
	vp := &realtime.Feed{Vehicles: []realtime.VehiclePosition{
		{TripID: "24E-0830", StartDate: "20261012", CurrentSequence: 7, Status: realtime.InTransitTo,
			Occupancy: realtime.OccupancyUnknown},
	}}
	got := Board(f, realtime.NewSnapshot(tu, vp), "52345", nil, at(f, "08:20:00"), 1, realtime.DefaultThresholds)
	// 7, 9, 10: stop 8 is skipped, and the trailing skipped stop 6 must not
	// make it look like the bus has already passed.
	if r := rows(got[0]); r != "24E-0830 08:31 on_time 3stops" {
		t.Errorf("got %s", r)
	}
}

func TestStopsAway(t *testing.T) {
	f := feed(t)
	cases := []struct {
		seq    uint32
		status realtime.VehicleStatus
		want   int
	}{
		{10, realtime.StoppedAt, 0},   // at our stop
		{9, realtime.StoppedAt, 1},    // at the previous stop
		{10, realtime.InTransitTo, 1}, // heading to ours next
		{7, realtime.InTransitTo, 4},  // 7, 8, 9, then ours
	}
	for _, c := range cases {
		vp := &realtime.Feed{Vehicles: []realtime.VehiclePosition{
			{TripID: "24E-0830", StartDate: "20261012", CurrentSequence: c.seq, Status: c.status},
		}}
		got := Board(f, realtime.NewSnapshot(nil, vp), "52345", nil, at(f, "08:20:00"), 1, realtime.DefaultThresholds)
		if a := got[0].Departures[0].StopsAway; a != c.want {
			t.Errorf("seq %d status %d: %d stops away, want %d", c.seq, c.status, a, c.want)
		}
	}
}

func TestSkippedStopAndNoData(t *testing.T) {
	f := feed(t)
	tu := &realtime.Feed{Trips: []realtime.TripUpdate{
		trip("24E-0815", realtime.StopUpdate{Sequence: 10, Relationship: realtime.StopSkipped}),
		trip("24E-0830", realtime.StopUpdate{Sequence: 10, Relationship: realtime.StopNoData}),
	}}
	got := Board(f, realtime.NewSnapshot(tu, nil), "52345", nil, at(f, "08:10:00"), 2, realtime.DefaultThresholds)
	want := "24E-0815 08:15 skipped | 24E-0830 08:30 scheduled | 24E-0845 08:45 scheduled"
	if r := rows(got[0]); r != want {
		t.Errorf("\n got: %s\nwant: %s", r, want)
	}
}

func TestOnTimeBand(t *testing.T) {
	f := feed(t)
	for offset, want := range map[time.Duration]realtime.Status{
		-59 * time.Second: realtime.OnTime,
		-60 * time.Second: realtime.Early,
		119 * time.Second: realtime.OnTime,
		120 * time.Second: realtime.Late,
	} {
		tu := &realtime.Feed{Trips: []realtime.TripUpdate{
			trip("24E-0830", stop(10, at(f, "08:30:00").Add(offset))),
		}}
		got := Board(f, realtime.NewSnapshot(tu, nil), "52345", nil, at(f, "08:20:00"), 1, realtime.DefaultThresholds)
		if s := got[0].Departures[0].Status; s != want {
			t.Errorf("offset %v: %s, want %s", offset, s, want)
		}
	}
}

func TestMetroIsScheduleOnly(t *testing.T) {
	f := feed(t)
	got := Board(f, realtime.NewSnapshot(nil, nil), "STATION-BERRI", nil, at(f, "08:02:00"), 3, realtime.DefaultThresholds)
	if len(got) != 2 {
		t.Fatalf("want both metro directions, got %d", len(got))
	}
	for _, g := range got {
		for _, d := range g.Departures {
			if d.Status != realtime.Scheduled {
				t.Errorf("metro departure with status %s", d.Status)
			}
		}
	}
}

func TestNilSnapshot(t *testing.T) {
	f := feed(t)
	got := Board(f, nil, "52345", nil, at(f, "08:01:00"), 3, realtime.DefaultThresholds)
	if r := rows(got[0]); r != "24E-0815 08:15 scheduled | 24E-0830 08:30 scheduled | 24E-0845 08:45 scheduled" {
		t.Errorf("got %s", r)
	}
}
