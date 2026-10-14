package gtfs

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
	"time"

	"stm-transit/internal/gtfs/gtfstest"
)

func loadFixture(t *testing.T) *Feed {
	t.Helper()
	b := gtfstest.Zip()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	loc, err := time.LoadLocation("America/Montreal")
	if err != nil {
		t.Fatal(err)
	}
	f, err := load(zr, loc)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func at(f *Feed, s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, f.Loc)
	if err != nil {
		panic(err)
	}
	return t
}

func times(g DirectionGroup) string {
	var parts []string
	for _, d := range g.Departures {
		parts = append(parts, d.Time.Format("15:04"))
	}
	return strings.Join(parts, " ")
}

func TestNextBusWeekday(t *testing.T) {
	f := loadFixture(t)
	// Monday 2026-10-12, 08:01: 08:00 has left, Saturday trip must not appear.
	g := f.Next("52345", at(f, "2026-10-12 08:01"), 3, nil)
	if len(g) != 1 {
		t.Fatalf("want 1 group, got %d", len(g))
	}
	if got := times(g[0]); got != "08:15 08:30 08:45" {
		t.Errorf("got %q", got)
	}
	if g[0].Headsign != "Est" || g[0].Departures[0].TripID != "24E-0815" {
		t.Errorf("unexpected group %+v", g[0])
	}
}

func TestNextRouteFilter(t *testing.T) {
	f := loadFixture(t)
	if g := f.Next("52345", at(f, "2026-10-12 08:01"), 3, []string{"51"}); len(g) != 0 {
		t.Errorf("filter for route 51 should return nothing, got %d groups", len(g))
	}
	if g := f.Next("52345", at(f, "2026-10-12 08:01"), 3, []string{"24"}); len(g) != 1 {
		t.Errorf("filter for route 24 should return 1 group, got %d", len(g))
	}
}

func TestNextSaturday(t *testing.T) {
	f := loadFixture(t)
	g := f.Next("52345", at(f, "2026-10-17 08:00"), 3, nil)
	if len(g) != 1 || times(g[0]) != "08:07" {
		t.Fatalf("Saturday: got %+v", g)
	}
}

func TestPastMidnightTrip(t *testing.T) {
	f := loadFixture(t)
	// Tuesday's own service is cancelled (calendar_dates), but Monday's
	// 25:10 trip still runs at 01:10 Tuesday.
	g := f.Next("52345", at(f, "2026-10-13 01:00"), 3, nil)
	if len(g) != 1 || times(g[0]) != "01:10" {
		t.Fatalf("got %+v", g)
	}
	if day := g[0].Departures[0].Time.Day(); day != 13 {
		t.Errorf("want Oct 13, got day %d", day)
	}
}

func TestCancelledDay(t *testing.T) {
	f := loadFixture(t)
	g := f.Next("52345", at(f, "2026-10-13 07:00"), 3, nil)
	if len(g) != 0 {
		t.Errorf("Tuesday service removed, got %+v", g)
	}
}

func TestMetroStationFrequencies(t *testing.T) {
	f := loadFixture(t)
	g := f.Next("STATION-BERRI", at(f, "2026-10-12 08:02"), 3, nil)
	if len(g) != 2 {
		t.Fatalf("want both directions, got %d groups", len(g))
	}
	// Northbound: trains leave Montmorency every 5 min from 05:30 and reach
	// Berri 10 min later, so 05:40, 05:45, ... 08:05.
	if got := times(g[0]); got != "08:05 08:10 08:15" {
		t.Errorf("northbound got %q", got)
	}
	// Southbound starts at Berri every 4 min from 05:30: ... 08:02, 08:06.
	if got := times(g[1]); got != "08:02 08:06 08:10" {
		t.Errorf("southbound got %q", got)
	}
	if !g[0].Departures[0].FromFrequency {
		t.Error("metro departures should be marked FromFrequency")
	}
}

func TestMetroAfterServiceWindow(t *testing.T) {
	f := loadFixture(t)
	// Windows end at 10:00; next service is Tuesday — cancelled in the fixture.
	if g := f.Next("STATION-BERRI", at(f, "2026-10-12 10:30"), 3, nil); len(g) != 0 {
		t.Errorf("expected no trains, got %+v", g)
	}
}

func TestDSTChangeDay(t *testing.T) {
	f := loadFixture(t)
	// Clocks fall back on Sun 2026-11-01. 08:00:00 must still be 8 a.m. local.
	g := f.Next("52345", at(f, "2026-11-01 07:00"), 3, nil)
	if len(g) != 1 {
		t.Fatalf("got %+v", g)
	}
	if h := g[0].Departures[0].Time.In(f.Loc).Hour(); h != 8 {
		t.Errorf("want 08:00 local, got hour %d", h)
	}
}

func TestSearchStops(t *testing.T) {
	f := loadFixture(t)
	got := f.SearchStops("cote", 10)
	if len(got) != 1 || got[0].ID != "CV" {
		t.Errorf("accent-insensitive search: got %+v", got)
	}
	got = f.SearchStops("berri", 10)
	if len(got) != 1 || got[0].ID != "STATION-BERRI" {
		t.Errorf("platforms should be hidden behind station: got %+v", got)
	}
	if got = f.SearchStops("52346", 10); len(got) != 1 {
		t.Errorf("search by stop code: got %+v", got)
	}
}

func TestNearbyStops(t *testing.T) {
	f := loadFixture(t)
	// A point ~100 m from the two Sherbrooke stops, ~700 m from Berri.
	got := f.NearbyStops(45.5177, -73.5682, 400, 10)
	if len(got) != 2 || got[0].ID != "52345" || got[0].Meters > 150 {
		t.Fatalf("got %+v", got)
	}
	got = f.NearbyStops(45.5177, -73.5682, 1000, 10)
	if len(got) != 3 || got[2].ID != "STATION-BERRI" {
		t.Errorf("station should appear once, not its platforms: %+v", got)
	}
}

func TestRoutesAt(t *testing.T) {
	f := loadFixture(t)
	if r := f.RoutesAt("STATION-BERRI"); len(r) != 1 || r[0].ShortName != "2" || !r[0].IsMetro() {
		t.Errorf("got %+v", r)
	}
}

func TestParseGTFSTime(t *testing.T) {
	for in, want := range map[string]int32{"08:00:00": 28800, "5:30:00": 19800, "25:10:00": 90600} {
		if got, err := ParseGTFSTime(in); err != nil || got != want {
			t.Errorf("%s: got %d, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "8:00", "08:60:00", "ab:00:00"} {
		if _, err := ParseGTFSTime(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}
