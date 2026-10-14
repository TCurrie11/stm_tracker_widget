// stm_tracker_widget: live STM bus and métro departures for KDE Plasma 6
// https://github.com/TCurrie11/stm_tracker_widget
//
// File:    internal/geo/geo.go
// Purpose: Turns addresses into coordinates (OpenStreetMap or Google).
// Author:  TCurrie11 (https://github.com/TCurrie11)
// Version: 0.1.0
// Created: 2026-10-14
// Updated: 2026-10-14
// License: GPL-2.0-or-later

// Package geo turns addresses into coordinates. The rest of the service
// only sees the Geocoder interface, so the provider is a config choice.
package geo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// Place is a geocoded address.
type Place struct {
	Lat, Lon float64
	Label    string // the provider's cleaned-up version of the address
}

// ErrNotFound means the provider understood the request but found nothing.
var ErrNotFound = errors.New("address not found")

// Geocoder looks up an address.
type Geocoder interface {
	Geocode(ctx context.Context, address string) (Place, error)
}

// Bias keeps results near the agency's area, so "Saint-Denis" finds the
// Montréal street rather than the town in France.
type Bias struct {
	MinLat, MinLon, MaxLat, MaxLon float64
}

// Montreal covers the island and the nearby suburbs.
var Montreal = Bias{MinLat: 45.35, MinLon: -74.05, MaxLat: 45.75, MaxLon: -73.45}

// ---- Nominatim (OpenStreetMap), free ----

// Nominatim uses the public OpenStreetMap geocoder. Its usage policy asks
// for at most one request per second and an identifying User-Agent; this
// is only called when someone types an address in the widget settings.
type Nominatim struct {
	BaseURL string // default https://nominatim.openstreetmap.org
	Contact string // email or URL, included in the User-Agent
	Bias    Bias
	Client  *http.Client

	mu          sync.Mutex
	lastRequest time.Time
}

func (n *Nominatim) Geocode(ctx context.Context, address string) (Place, error) {
	n.mu.Lock()
	if wait := time.Second - time.Since(n.lastRequest); wait > 0 {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			n.mu.Unlock()
			return Place{}, ctx.Err()
		}
	}
	n.lastRequest = time.Now()
	n.mu.Unlock()

	base := n.BaseURL
	if base == "" {
		base = "https://nominatim.openstreetmap.org"
	}
	query := url.Values{
		"q": {address}, "format": {"jsonv2"}, "limit": {"1"},
		"viewbox": {fmt.Sprintf("%f,%f,%f,%f", n.Bias.MinLon, n.Bias.MaxLat, n.Bias.MaxLon, n.Bias.MinLat)},
		"bounded": {"1"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/search?"+query.Encode(), nil)
	if err != nil {
		return Place{}, err
	}
	userAgent := "stm-transit-widget/0.1"
	if n.Contact != "" {
		userAgent += " (" + n.Contact + ")"
	}
	req.Header.Set("User-Agent", userAgent)

	var results []struct {
		Lat, Lon    string
		DisplayName string `json:"display_name"`
	}
	if err := getJSON(n.Client, req, &results); err != nil {
		return Place{}, fmt.Errorf("nominatim: %w", err)
	}
	if len(results) == 0 {
		return Place{}, ErrNotFound
	}
	lat, err1 := strconv.ParseFloat(results[0].Lat, 64)
	lon, err2 := strconv.ParseFloat(results[0].Lon, 64)
	if err1 != nil || err2 != nil {
		return Place{}, fmt.Errorf("nominatim: bad coordinates %q,%q", results[0].Lat, results[0].Lon)
	}
	return Place{Lat: lat, Lon: lon, Label: results[0].DisplayName}, nil
}

// ---- Google Geocoding API, optional ----

// Google uses the Google Maps Geocoding API (needs a key and billing
// account; settings-page use stays well inside the free monthly allowance).
type Google struct {
	BaseURL string // default https://maps.googleapis.com
	Key     string
	Bias    Bias
	Client  *http.Client
}

func (g *Google) Geocode(ctx context.Context, address string) (Place, error) {
	base := g.BaseURL
	if base == "" {
		base = "https://maps.googleapis.com"
	}
	query := url.Values{
		"address": {address}, "key": {g.Key},
		"bounds": {fmt.Sprintf("%f,%f|%f,%f", g.Bias.MinLat, g.Bias.MinLon, g.Bias.MaxLat, g.Bias.MaxLon)},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/maps/api/geocode/json?"+query.Encode(), nil)
	if err != nil {
		return Place{}, err
	}
	var response struct {
		Status  string
		Results []struct {
			FormattedAddress string `json:"formatted_address"`
			Geometry         struct {
				Location struct{ Lat, Lng float64 }
			}
		}
		ErrorMessage string `json:"error_message"`
	}
	if err := getJSON(g.Client, req, &response); err != nil {
		return Place{}, fmt.Errorf("google geocode: %w", err)
	}
	switch response.Status {
	case "OK":
	case "ZERO_RESULTS":
		return Place{}, ErrNotFound
	default:
		return Place{}, fmt.Errorf("google geocode: %s %s", response.Status, response.ErrorMessage)
	}
	best := response.Results[0]
	return Place{Lat: best.Geometry.Location.Lat, Lon: best.Geometry.Location.Lng, Label: best.FormattedAddress}, nil
}

func getJSON(client *http.Client, req *http.Request, target any) error {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(target)
}
