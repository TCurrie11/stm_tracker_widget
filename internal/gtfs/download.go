// stm_tracker_widget: live STM bus and métro departures for KDE Plasma 6
// https://github.com/TCurrie11/stm_tracker_widget
//
// File:    internal/gtfs/download.go
// Purpose: Downloads the agency's GTFS schedule zip safely.
// Author:  TCurrie11 (https://github.com/TCurrie11)
// Version: 0.1.0
// Created: 2026-10-14
// Updated: 2026-10-14
// License: GPL-2.0-or-later

package gtfs

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// Download saves the GTFS zip at url to path and returns its size. It
// writes to a temp file and renames, so a failed download never leaves a
// half-written zip where the service expects a good one.
func Download(ctx context.Context, url, path string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("download: %s", resp.Status)
	}
	partial, err := os.CreateTemp(filepath.Dir(path), "gtfs-*.zip")
	if err != nil {
		return 0, err
	}
	defer os.Remove(partial.Name())
	n, err := io.Copy(partial, resp.Body)
	if closeErr := partial.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return 0, err
	}
	return n, os.Rename(partial.Name(), path)
}
