// stm_tracker_widget: live STM bus and métro departures for KDE Plasma 6
// https://github.com/TCurrie11/stm_tracker_widget
//
// File:    internal/config/config_test.go
// Purpose: Tests for settings loading and validation.
// Author:  TCurrie11 (https://github.com/TCurrie11)
// Version: 0.1.0
// Created: 2026-10-14
// Updated: 2026-10-14
// License: GPL-2.0-or-later

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMissingFileGivesDefaults(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Agencies[0].ID != "stm" || c.PollInterval.D() != time.Minute {
		t.Errorf("got %+v", c)
	}
}

func TestPartialFileKeepsDefaults(t *testing.T) {
	c, err := Load(write(t, `{"poll_interval": "90s"}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.PollInterval.D() != 90*time.Second || c.WakeLead.D() != 30*time.Minute || len(c.Agencies) != 1 {
		t.Errorf("got %+v", c)
	}
}

func TestSecondAgency(t *testing.T) {
	c, err := Load(write(t, `{"agencies": [
		{"id":"stm","timezone":"America/Montreal","gtfs_url":"https://a/stm.zip"},
		{"id":"exo","timezone":"America/Montreal","gtfs_url":"https://a/exo.zip"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Agencies) != 2 || c.Agencies[1].ID != "exo" {
		t.Errorf("got %+v", c.Agencies)
	}
}

func TestKeyFromEnv(t *testing.T) {
	t.Setenv("STM_API_KEY", "secret")
	if k := Default().Agencies[0].Key(); k != "secret" {
		t.Errorf("got %q", k)
	}
}

func TestInvalid(t *testing.T) {
	for body, want := range map[string]string{
		`{"geocoder":{"provider":"google"}}`:                              "google_key",
		`{"geocoder":{"provider":"bing"}}`:                                "unknown geocoder",
		`{"poll_interval":"5s"}`:                                          "at least 15s",
		`{"poll_interval":60}`:                                            "string like",
		`{"agencies":[{"id":"x","timezone":"Mars/Base","gtfs_url":"u"}]}`: "Mars/Base",
		`{"agencies":[{"id":"x","timezone":"UTC","gtfs_url":"u"},{"id":"x","timezone":"UTC","gtfs_url":"u"}]}`: "twice",
	} {
		_, err := Load(write(t, body))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want error containing %q, got %v", body, want, err)
		}
	}
}
