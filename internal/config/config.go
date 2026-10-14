// Package config loads transitd's settings from a JSON file.
//
// Everything agency-specific lives here, so supporting another GTFS agency
// (exo, STL, RTL, ...) means adding an agency entry, not writing code.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Agency describes one transit agency's data sources.
type Agency struct {
	ID       string `json:"id"`       // short name, e.g. "stm"
	Name     string `json:"name"`     // shown in the widget's attribution line
	Timezone string `json:"timezone"` // IANA zone the schedule is written in

	GTFSURL string `json:"gtfs_url"` // static schedule zip

	// GTFS-Realtime feeds. Empty means schedule-only (the widget then shows
	// "scheduled" instead of live times).
	TripUpdatesURL      string `json:"trip_updates_url,omitempty"`
	VehiclePositionsURL string `json:"vehicle_positions_url,omitempty"`

	// How the API key is sent: as an HTTP header (APIKeyHeader) or a query
	// parameter (APIKeyParam). The key itself is APIKey or, if that is
	// empty, the environment variable named by APIKeyEnv.
	APIKeyHeader string `json:"api_key_header,omitempty"`
	APIKeyParam  string `json:"api_key_param,omitempty"`
	APIKey       string `json:"api_key,omitempty"`
	APIKeyEnv    string `json:"api_key_env,omitempty"`

	// Requests per day the agency allows across all realtime feeds.
	DailyRequestLimit int `json:"daily_request_limit,omitempty"`
}

// Key returns the agency API key from the config or the environment.
func (a Agency) Key() string {
	if a.APIKey != "" {
		return a.APIKey
	}
	if a.APIKeyEnv != "" {
		return os.Getenv(a.APIKeyEnv)
	}
	return ""
}

// Geocoder selects how addresses are turned into coordinates.
type Geocoder struct {
	Provider  string `json:"provider"`             // "nominatim" (default) or "google"
	GoogleKey string `json:"google_key,omitempty"` // only for provider "google"
	// Contact for Nominatim's usage policy, sent in the User-Agent.
	Contact string `json:"contact,omitempty"`
}

// Config is the whole settings file.
type Config struct {
	Listen   string   `json:"listen"` // HTTP address, localhost only
	Agencies []Agency `json:"agencies"`
	Geocoder Geocoder `json:"geocoder"`

	// PollInterval is how often realtime feeds are fetched while active.
	PollInterval Duration `json:"poll_interval"`
	// WakeLead is how long before the first departure on a watched route the
	// service resumes polling after an overnight pause.
	WakeLead Duration `json:"wake_lead"`
	// IdleAfter stops polling when the widget hasn't asked for anything in
	// this long (closed widget, plasma not running).
	IdleAfter Duration `json:"idle_after"`
}

// Default returns a working STM configuration. The API key still has to be
// supplied, via the file or the STM_API_KEY environment variable.
func Default() Config {
	return Config{
		Listen: "127.0.0.1:8765",
		Agencies: []Agency{{
			ID:                "stm",
			Name:              "Société de transport de Montréal",
			Timezone:          "America/Montreal",
			GTFSURL:           "https://www.stm.info/sites/default/files/gtfs/gtfs_stm.zip",
			APIKeyEnv:         "STM_API_KEY",
			DailyRequestLimit: 10000,
		}},
		Geocoder:     Geocoder{Provider: "nominatim"},
		PollInterval: Duration(time.Minute),
		WakeLead:     Duration(30 * time.Minute),
		IdleAfter:    Duration(10 * time.Minute),
	}
}

// DefaultPath is ~/.config/stm-transit/config.json.
func DefaultPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "stm-transit", "config.json")
}

// Load reads path over the defaults. A missing file is not an error: the
// defaults are returned so `transitd` works out of the box.
func Load(path string) (Config, error) {
	c := Default()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	// Agencies in the file replace the default list rather than merging into
	// it, so decode them separately and only override when present.
	c.Agencies = nil
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	if len(c.Agencies) == 0 {
		c.Agencies = Default().Agencies
	}
	return c, c.Validate()
}

// Validate checks for settings that would fail later in confusing ways.
func (c Config) Validate() error {
	seen := map[string]bool{}
	for i, a := range c.Agencies {
		if a.ID == "" || a.GTFSURL == "" || a.Timezone == "" {
			return fmt.Errorf("agency %d: id, gtfs_url and timezone are required", i)
		}
		if seen[a.ID] {
			return fmt.Errorf("agency %q listed twice", a.ID)
		}
		seen[a.ID] = true
		if _, err := time.LoadLocation(a.Timezone); err != nil {
			return fmt.Errorf("agency %q: %w", a.ID, err)
		}
	}
	switch c.Geocoder.Provider {
	case "", "nominatim":
	case "google":
		if c.Geocoder.GoogleKey == "" {
			return errors.New(`geocoder "google" needs google_key`)
		}
	default:
		return fmt.Errorf("unknown geocoder %q", c.Geocoder.Provider)
	}
	if c.PollInterval.D() < 15*time.Second {
		return errors.New("poll_interval must be at least 15s")
	}
	return nil
}

// Duration is a time.Duration written as "60s" or "30m" in JSON.
type Duration time.Duration

// D returns the value as a time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("duration must be a string like \"60s\": %w", err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}
