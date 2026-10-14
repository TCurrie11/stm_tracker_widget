// stm_tracker_widget: live STM bus and métro departures for KDE Plasma 6
// https://github.com/TCurrie11/stm_tracker_widget
//
// File:    internal/gtfs/load.go
// Purpose: Reads a GTFS zip into the indexed in-memory schedule.
// Author:  TCurrie11 (https://github.com/TCurrie11)
// Version: 0.1.0
// Created: 2026-10-12
// Updated: 2026-10-14
// License: GPL-2.0-or-later

package gtfs

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// Load reads a GTFS zip file. timeZone is the agency's time zone
// (America/Montreal for STM); schedule times are interpreted in it.
func Load(path string, timeZone *time.Location) (*Feed, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("open gtfs zip: %w", err)
	}
	defer archive.Close()
	return load(&archive.Reader, timeZone)
}

func load(archive *zip.Reader, timeZone *time.Location) (*Feed, error) {
	feed := &Feed{
		Stops:              map[string]*Stop{},
		routeIndexByID:     map[string]int32{},
		tripIndexByID:      map[string]int32{},
		stopTimesByStop:    map[string][]stopTime{},
		frequenciesByTrip:  map[int32][]frequency{},
		services:           map[string]service{},
		servicesAddedOn:    map[string]map[string]bool{},
		servicesRemovedOn:  map[string]map[string]bool{},
		platformsByStation: map[string][]string{},
		TimeZone:           timeZone,
	}
	filesByName := map[string]*zip.File{}
	for _, file := range archive.File {
		filesByName[file.Name] = file
	}

	// Order matters: trips need routes, stop_times need trips.
	tables := []struct {
		name      string
		required  bool
		addRecord func(csvRecord) error
	}{
		{"stops.txt", true, feed.addStop},
		{"routes.txt", true, feed.addRoute},
		{"trips.txt", true, feed.addTrip},
		{"stop_times.txt", true, feed.addStopTime},
		{"calendar.txt", false, feed.addCalendar},
		{"calendar_dates.txt", false, feed.addCalendarDate},
		{"frequencies.txt", false, feed.addFrequency},
	}
	for _, table := range tables {
		file, ok := filesByName[table.name]
		if !ok {
			if table.required {
				return nil, fmt.Errorf("gtfs: missing %s", table.name)
			}
			continue
		}
		if table.name == "stop_times.txt" {
			feed.tripFirstDeparture = make([]int32, len(feed.trips))
			for i := range feed.tripFirstDeparture {
				feed.tripFirstDeparture[i] = -1
			}
		}
		if err := readCSV(file, table.addRecord); err != nil {
			return nil, fmt.Errorf("gtfs %s: %w", table.name, err)
		}
	}
	if len(feed.services) == 0 && len(feed.servicesAddedOn) == 0 {
		return nil, errors.New("gtfs: neither calendar.txt nor calendar_dates.txt has data")
	}
	for stopID, stop := range feed.Stops {
		if stop.Parent != "" {
			feed.platformsByStation[stop.Parent] = append(feed.platformsByStation[stop.Parent], stopID)
		}
	}
	return feed, nil
}

// csvRecord gives access to a CSV record by column name.
type csvRecord struct {
	columnIndex map[string]int
	values      []string
}

func (record csvRecord) get(name string) string {
	column, ok := record.columnIndex[name]
	if !ok || column >= len(record.values) {
		return ""
	}
	return strings.TrimSpace(record.values[column])
}

func readCSV(file *zip.File, addRecord func(csvRecord) error) error {
	contents, err := file.Open()
	if err != nil {
		return err
	}
	defer contents.Close()
	reader := csv.NewReader(contents)
	reader.LazyQuotes = true
	reader.FieldsPerRecord = -1
	reader.ReuseRecord = true
	header, err := reader.Read()
	if err != nil {
		return err
	}
	columnIndex := map[string]int{}
	for column, name := range header {
		columnIndex[strings.TrimSpace(strings.TrimPrefix(name, "\ufeff"))] = column
	}
	lineNumber := 1
	for {
		values, err := reader.Read()
		if err == io.EOF {
			return nil
		}
		lineNumber++
		if err != nil {
			return fmt.Errorf("line %d: %w", lineNumber, err)
		}
		if err := addRecord(csvRecord{columnIndex, values}); err != nil {
			return fmt.Errorf("line %d: %w", lineNumber, err)
		}
	}
}

func (f *Feed) addStop(record csvRecord) error {
	lat, _ := strconv.ParseFloat(record.get("stop_lat"), 64)
	lon, _ := strconv.ParseFloat(record.get("stop_lon"), 64)
	stopID := record.get("stop_id")
	f.Stops[stopID] = &Stop{
		ID: stopID, Code: record.get("stop_code"), Name: record.get("stop_name"),
		Lat: lat, Lon: lon, Parent: record.get("parent_station"),
		IsStation: record.get("location_type") == "1",
	}
	return nil
}

func (f *Feed) addRoute(record csvRecord) error {
	routeType, _ := strconv.Atoi(record.get("route_type"))
	routeID := record.get("route_id")
	f.routeIndexByID[routeID] = int32(len(f.Routes))
	f.Routes = append(f.Routes, Route{
		ID: routeID, ShortName: record.get("route_short_name"), LongName: record.get("route_long_name"),
		Type: routeType, Color: record.get("route_color"),
	})
	return nil
}

func (f *Feed) addTrip(record csvRecord) error {
	routeIndex, ok := f.routeIndexByID[record.get("route_id")]
	if !ok {
		return nil // trip for an unknown route; skip rather than fail
	}
	direction := int8(-1)
	switch record.get("direction_id") {
	case "0":
		direction = 0
	case "1":
		direction = 1
	}
	f.tripIndexByID[record.get("trip_id")] = int32(len(f.trips))
	f.tripIDs = append(f.tripIDs, record.get("trip_id"))
	f.trips = append(f.trips, trip{routeIndex: routeIndex, serviceID: record.get("service_id"),
		headsign: record.get("trip_headsign"), direction: direction})
	return nil
}

func (f *Feed) addStopTime(record csvRecord) error {
	tripIndex, ok := f.tripIndexByID[record.get("trip_id")]
	if !ok {
		return nil
	}
	timeText := record.get("departure_time")
	if timeText == "" {
		timeText = record.get("arrival_time")
	}
	if timeText == "" {
		return nil // untimed stop; GTFS allows interpolation, we just skip it
	}
	seconds, err := ParseGTFSTime(timeText)
	if err != nil {
		return err
	}
	sequence, _ := strconv.Atoi(record.get("stop_sequence"))
	stopID := record.get("stop_id")
	f.stopTimesByStop[stopID] = append(f.stopTimesByStop[stopID], stopTime{tripIndex: tripIndex, departure: seconds, sequence: int32(sequence)})
	if f.tripFirstDeparture[tripIndex] < 0 || seconds < f.tripFirstDeparture[tripIndex] {
		f.tripFirstDeparture[tripIndex] = seconds
	}
	return nil
}

func (f *Feed) addCalendar(record csvRecord) error {
	var calendar service
	for weekday, dayName := range []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"} {
		calendar.days[weekday] = record.get(dayName) == "1"
	}
	calendar.start, calendar.end = record.get("start_date"), record.get("end_date")
	f.services[record.get("service_id")] = calendar
	return nil
}

func (f *Feed) addCalendarDate(record csvRecord) error {
	date, serviceID := record.get("date"), record.get("service_id")
	exceptions := f.servicesAddedOn
	if record.get("exception_type") == "2" {
		exceptions = f.servicesRemovedOn
	}
	if exceptions[date] == nil {
		exceptions[date] = map[string]bool{}
	}
	exceptions[date][serviceID] = true
	return nil
}

func (f *Feed) addFrequency(record csvRecord) error {
	tripIndex, ok := f.tripIndexByID[record.get("trip_id")]
	if !ok {
		return nil
	}
	start, err := ParseGTFSTime(record.get("start_time"))
	if err != nil {
		return err
	}
	end, err := ParseGTFSTime(record.get("end_time"))
	if err != nil {
		return err
	}
	headway, err := strconv.Atoi(record.get("headway_secs"))
	if err != nil || headway <= 0 {
		return fmt.Errorf("bad headway_secs %q", record.get("headway_secs"))
	}
	f.frequenciesByTrip[tripIndex] = append(f.frequenciesByTrip[tripIndex], frequency{start, end, int32(headway)})
	return nil
}

// ParseGTFSTime parses "H:MM:SS" or "HH:MM:SS" into seconds after midnight.
// Hours may be 24 or more for trips that run past midnight.
func ParseGTFSTime(text string) (int32, error) {
	parts := strings.Split(text, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("bad time %q", text)
	}
	var hms [3]int
	for i, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return 0, fmt.Errorf("bad time %q", text)
		}
		hms[i] = value
	}
	if hms[1] > 59 || hms[2] > 59 {
		return 0, fmt.Errorf("bad time %q", text)
	}
	return int32(hms[0]*3600 + hms[1]*60 + hms[2]), nil
}

// LoadBytes reads a GTFS zip held in memory.
func LoadBytes(zipData []byte, timeZone *time.Location) (*Feed, error) {
	archive, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("open gtfs zip: %w", err)
	}
	return load(archive, timeZone)
}
