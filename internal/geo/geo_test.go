// stm_tracker_widget: live STM bus and métro departures for KDE Plasma 6
// https://github.com/TCurrie11/stm_tracker_widget
//
// File:    internal/geo/geo_test.go
// Purpose: Tests for the geocoders.
// Author:  TCurrie11 (https://github.com/TCurrie11)
// Version: 0.1.0
// Created: 2026-10-14
// Updated: 2026-10-14
// License: GPL-2.0-or-later

package geo

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNominatim(t *testing.T) {
	var gotUA, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA, gotQuery = r.UserAgent(), r.URL.Query().Get("q")
		if strings.Contains(gotQuery, "nowhere") {
			w.Write([]byte(`[]`))
			return
		}
		w.Write([]byte(`[{"lat":"45.5152","lon":"-73.5610","display_name":"Berri-UQAM, Montréal"}]`))
	}))
	defer srv.Close()

	n := &Nominatim{BaseURL: srv.URL, Contact: "me@example.com", Bias: Montreal}
	p, err := n.Geocode(context.Background(), "1500 Berri")
	if err != nil {
		t.Fatal(err)
	}
	if p.Lat != 45.5152 || p.Lon != -73.5610 || p.Label != "Berri-UQAM, Montréal" {
		t.Errorf("got %+v", p)
	}
	if !strings.Contains(gotUA, "me@example.com") || gotQuery != "1500 Berri" {
		t.Errorf("ua=%q q=%q", gotUA, gotQuery)
	}
	if _, err := n.Geocode(context.Background(), "nowhere"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestGoogle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("key") {
		case "bad":
			w.Write([]byte(`{"status":"REQUEST_DENIED","error_message":"invalid key"}`))
		default:
			w.Write([]byte(`{"status":"OK","results":[{"formatted_address":"Berri, Montréal","geometry":{"location":{"lat":45.5,"lng":-73.5}}}]}`))
		}
	}))
	defer srv.Close()

	g := &Google{BaseURL: srv.URL, Key: "good", Bias: Montreal}
	p, err := g.Geocode(context.Background(), "Berri")
	if err != nil || p.Lat != 45.5 || p.Lon != -73.5 {
		t.Fatalf("got %+v, %v", p, err)
	}
	g.Key = "bad"
	if _, err := g.Geocode(context.Background(), "Berri"); err == nil || !strings.Contains(err.Error(), "REQUEST_DENIED") {
		t.Errorf("want REQUEST_DENIED, got %v", err)
	}
}

func TestHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	n := &Nominatim{BaseURL: srv.URL}
	if _, err := n.Geocode(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "429") {
		t.Errorf("want 429 error, got %v", err)
	}
}
