// stm_tracker_widget: live STM bus and métro departures for KDE Plasma 6
// https://github.com/TCurrie11/stm_tracker_widget
//
// File:    internal/realtime/fetch.go
// Purpose: Fetches live feeds within STM's daily budget, with backoff.
// Author:  TCurrie11 (https://github.com/TCurrie11)
// Version: 0.1.0
// Created: 2026-10-14
// Updated: 2026-10-14
// License: GPL-2.0-or-later

package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Errors a caller can react to. The widget shows schedule times with a
// short note in every one of these cases; the details go to the log.
var (
	ErrNoKey           = errors.New("no API key configured")
	ErrBudgetExhausted = errors.New("daily request budget used up")
	ErrUnauthorized    = errors.New("API key rejected")
)

// BackoffError means a previous failure put fetching on hold.
type BackoffError struct{ Until time.Time }

func (e *BackoffError) Error() string {
	return "backing off until " + e.Until.Format("15:04:05")
}

// Auth says how the API key is sent.
type Auth struct {
	Key    string
	Header string // e.g. "apikey"; used when set
	Param  string // query parameter name; used when Header is empty
}

// Fetcher downloads GTFS-Realtime feeds politely: within a daily request
// budget, backing off after failures, and never exposing the key in errors.
type Fetcher struct {
	Client *http.Client
	Auth   Auth
	Budget *Budget

	mu       sync.Mutex
	failures int
	retryAt  time.Time
	now      func() time.Time // for tests
}

const (
	maxFeedBytes = 64 << 20
	backoffBase  = 30 * time.Second
	backoffMax   = 15 * time.Minute
	authBackoff  = 10 * time.Minute // a rejected key won't fix itself quickly
)

func (f *Fetcher) clock() time.Time {
	if f.now != nil {
		return f.now()
	}
	return time.Now()
}

// Fetch downloads and decodes one feed. name is used in errors instead of
// the URL, which may carry the key.
func (f *Fetcher) Fetch(ctx context.Context, name, feedURL string) (*Feed, error) {
	if f.Auth.Key == "" {
		return nil, ErrNoKey
	}
	f.mu.Lock()
	if until := f.retryAt; f.clock().Before(until) {
		f.mu.Unlock()
		return nil, fmt.Errorf("%s: %w", name, &BackoffError{until})
	}
	f.mu.Unlock()
	if f.Budget != nil {
		if err := f.Budget.Take(); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: bad URL", name)
	}
	if f.Auth.Header != "" {
		req.Header.Set(f.Auth.Header, f.Auth.Key)
	} else if f.Auth.Param != "" {
		query := req.URL.Query()
		query.Set(f.Auth.Param, f.Auth.Key)
		req.URL.RawQuery = query.Encode()
	}
	req.Header.Set("Accept", "application/x-protobuf, application/octet-stream")

	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		// *url.Error's message includes the full URL, query string and all.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		f.fail(0)
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		f.fail(authBackoff)
		return nil, fmt.Errorf("%s: %w (HTTP %d)", name, ErrUnauthorized, resp.StatusCode)
	case resp.StatusCode == http.StatusTooManyRequests:
		f.fail(retryAfter(resp.Header.Get("Retry-After")))
		return nil, fmt.Errorf("%s: rate limited (HTTP 429)", name)
	default:
		f.fail(0)
		return nil, fmt.Errorf("%s: HTTP %d", name, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFeedBytes+1))
	if err != nil {
		f.fail(0)
		return nil, fmt.Errorf("%s: reading body: %w", name, err)
	}
	if len(body) > maxFeedBytes {
		return nil, fmt.Errorf("%s: feed larger than %d MB", name, maxFeedBytes>>20)
	}
	feed, err := Decode(body)
	if err != nil {
		// An HTML error page with status 200 lands here.
		f.fail(0)
		return nil, fmt.Errorf("%s: decoding: %w", name, err)
	}
	f.mu.Lock()
	f.failures, f.retryAt = 0, time.Time{}
	f.mu.Unlock()
	return feed, nil
}

// fail schedules the next attempt. wait 0 means exponential backoff:
// 30s, 1m, 2m, 4m, ... up to 15m.
func (f *Fetcher) fail(wait time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures++
	if wait == 0 {
		wait = backoffBase << min(f.failures-1, 10)
	}
	f.retryAt = f.clock().Add(min(wait, backoffMax))
}

func retryAfter(header string) time.Duration {
	if seconds, err := strconv.Atoi(header); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return 0
}

// Budget counts requests per day (in the agency's time zone) and refuses
// once the day's allowance, minus a safety reserve, is used. The count is
// saved to disk so restarting the service doesn't reset it.
type Budget struct {
	limit    int
	path     string
	timeZone *time.Location
	now      func() time.Time

	mu    sync.Mutex
	state budgetState
}

type budgetState struct {
	Day   string `json:"day"`
	Count int    `json:"count"`
}

// NewBudget loads the saved count from path, if any. limit 0 means
// unlimited. path "" keeps the count in memory only.
func NewBudget(path string, limit int, loc *time.Location) *Budget {
	budget := &Budget{limit: limit, path: path, timeZone: loc}
	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(data, &budget.state) // a corrupt file just starts from 0
		}
	}
	return budget
}

// stopAt keeps 5% of the allowance in reserve, so a manual test with curl
// on a heavy day doesn't push the account over.
func (b *Budget) stopAt() int { return b.limit - b.limit/20 }

// Take uses one request, or fails with ErrBudgetExhausted.
func (b *Budget) Take() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now
	if b.now != nil {
		now = b.now
	}
	day := now().In(b.timeZone).Format("2006-01-02")
	if b.state.Day != day {
		b.state = budgetState{Day: day}
	}
	if b.limit > 0 && b.state.Count >= b.stopAt() {
		return ErrBudgetExhausted
	}
	b.state.Count++
	b.save()
	return nil
}

// Used returns today's count and the point at which fetching stops.
func (b *Budget) Used() (count, stopAt int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state.Count, b.stopAt()
}

func (b *Budget) save() {
	if b.path == "" {
		return
	}
	data, _ := json.Marshal(b.state)
	_ = os.MkdirAll(filepath.Dir(b.path), 0o755)
	tmp := b.path + ".tmp"
	if os.WriteFile(tmp, data, 0o644) == nil {
		_ = os.Rename(tmp, b.path)
	}
}
