package gtfs

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
	"time"
)

// A tiny feed shaped like STM's: a bus route with a stop per direction, and
// a headway-based metro line with a parent station and two platforms.
var fixture = map[string]string{
	"stops.txt": `stop_id,stop_code,stop_name,stop_lat,stop_lon,location_type,parent_station
52345,52345,Sherbrooke / Saint-Denis,45.5185,-73.5690,0,
52346,52346,Sherbrooke / Saint-Denis,45.5186,-73.5692,0,
STATION-BERRI,,Station Berri-UQAM,45.5152,-73.5610,1,
BERRI-N,,Berri-UQAM (Côte-Vertu),45.5152,-73.5610,0,STATION-BERRI
BERRI-S,,Berri-UQAM (Montmorency),45.5152,-73.5610,0,STATION-BERRI
CV,,Station Côte-Vertu,45.5141,-73.6830,1,
`,
	"routes.txt": `route_id,route_short_name,route_long_name,route_type,route_color
24,24,Sherbrooke,3,009EE0
2,2,Orange,1,EF8122
`,
	"trips.txt": `route_id,service_id,trip_id,trip_headsign,direction_id
24,WK,24E-0800,Est,0
24,WK,24E-0815,Est,0
24,WK,24E-0830,Est,0
24,WK,24E-0845,Est,0
24,WK,24E-2510,Est,0
24,SA,24E-SAT,Est,0
24,WK,24O-0805,Ouest,1
24,SUN,24E-DST,Est,0
2,WK,O-N,Côte-Vertu,0
2,WK,O-S,Montmorency,1
`,
	"stop_times.txt": "\ufefftrip_id,arrival_time,departure_time,stop_id,stop_sequence\n" + `24E-0800,08:00:00,08:00:00,52345,10
24E-0815,08:15:00,08:15:00,52345,10
24E-0830,08:30:00,08:30:00,52345,10
24E-0845,08:45:00,08:45:00,52345,10
24E-2510,25:10:00,25:10:00,52345,10
24E-SAT,08:07:00,08:07:00,52345,10
24E-DST,08:00:00,08:00:00,52345,10
24O-0805,08:05:00,08:05:00,52346,10
O-N,05:30:00,05:30:00,MONTMO,1
O-N,05:40:00,05:40:00,BERRI-N,8
O-S,05:30:00,05:30:00,BERRI-S,1
`,
	"calendar.txt": `service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date
WK,1,1,1,1,1,0,0,20260824,20261025
SA,0,0,0,0,0,1,0,20260824,20261025
`,
	"calendar_dates.txt": `service_id,date,exception_type
WK,20261013,2
SUN,20261101,1
`,
	"frequencies.txt": `trip_id,start_time,end_time,headway_secs
O-N,05:30:00,10:00:00,300
O-S,05:30:00,10:00:00,240
`,
}

func loadFixture(t *testing.T) *Feed {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range fixture {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	zw.Close()
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
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
