// stm_tracker_widget: live STM bus and métro departures for KDE Plasma 6
// https://github.com/TCurrie11/stm_tracker_widget
//
// File:    internal/gtfs/gtfstest/gtfstest.go
// Purpose: A small STM-shaped GTFS feed for tests.
// Author:  TCurrie11 (https://github.com/TCurrie11)
// Version: 0.1.0
// Created: 2026-10-14
// Updated: 2026-10-14
// License: GPL-2.0-or-later

// Package gtfstest provides a small GTFS feed for tests, shaped like STM's.
//
// Monday 2026-10-12 is a normal weekday; Tuesday 2026-10-13 has its weekday
// service removed; route 24 runs at stop 52345 (Est) and 52346 (Ouest);
// the Orange line (route 2) runs by frequency through STATION-BERRI.
package gtfstest

import (
	"archive/zip"
	"bytes"
)

// Files is a tiny feed shaped like STM's: a bus route with a stop per direction, and
// a headway-based metro line with a parent station and two platforms.
var Files = map[string]string{
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

// Zip returns Files packed as a GTFS zip.
func Zip() []byte {
	var buf bytes.Buffer
	zipWriter := zip.NewWriter(&buf)
	for name, body := range Files {
		w, err := zipWriter.Create(name)
		if err != nil {
			panic(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			panic(err)
		}
	}
	if err := zipWriter.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}
