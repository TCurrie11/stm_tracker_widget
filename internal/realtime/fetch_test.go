package realtime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const secret = "sk-test-SECRET-1234"

// feedServer serves the real vehicle positions sample, checking the key.
func feedServer(t *testing.T, status *atomic.Int32) *httptest.Server {
	body := readGz(t, "vehiclepositions.pb.gz")
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s := int(status.Load()); s != 0 {
			if s == http.StatusTooManyRequests {
				w.Header().Set("Retry-After", "120")
			}
			w.WriteHeader(s)
			return
		}
		if r.Header.Get("apikey") != secret && r.URL.Query().Get("key") != secret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write(body)
	}))
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func TestFetchWithHeaderKey(t *testing.T) {
	var status atomic.Int32
	srv := feedServer(t, &status)
	defer srv.Close()
	f := &Fetcher{Auth: Auth{Key: secret, Header: "apikey"}}
	feed, err := f.Fetch(context.Background(), "vehicle positions", srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(feed.Vehicles) != 578 {
		t.Errorf("got %d vehicles", len(feed.Vehicles))
	}
}

func TestFetchWithQueryKey(t *testing.T) {
	var status atomic.Int32
	srv := feedServer(t, &status)
	defer srv.Close()
	f := &Fetcher{Auth: Auth{Key: secret, Param: "key"}}
	if _, err := f.Fetch(context.Background(), "vp", srv.URL); err != nil {
		t.Fatal(err)
	}
}

func TestFetchNoKey(t *testing.T) {
	f := &Fetcher{}
	if _, err := f.Fetch(context.Background(), "vp", "http://unused"); !errors.Is(err, ErrNoKey) {
		t.Errorf("got %v", err)
	}
}

func TestBackoffAfterFailures(t *testing.T) {
	var status atomic.Int32
	srv := feedServer(t, &status)
	defer srv.Close()
	clk := &fakeClock{t: time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)}
	f := &Fetcher{Auth: Auth{Key: secret, Header: "apikey"}, now: clk.now}

	status.Store(http.StatusServiceUnavailable)
	if _, err := f.Fetch(context.Background(), "vp", srv.URL); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("want 503 error, got %v", err)
	}
	// Within 30s: refused without touching the network.
	status.Store(0)
	var be *BackoffError
	if _, err := f.Fetch(context.Background(), "vp", srv.URL); !errors.As(err, &be) {
		t.Fatalf("want BackoffError, got %v", err)
	}
	clk.advance(31 * time.Second)
	if _, err := f.Fetch(context.Background(), "vp", srv.URL); err != nil {
		t.Fatalf("should recover after backoff: %v", err)
	}

	// 429 with Retry-After: 120 waits 2 minutes, not 30s.
	status.Store(http.StatusTooManyRequests)
	f.Fetch(context.Background(), "vp", srv.URL)
	status.Store(0)
	clk.advance(90 * time.Second)
	if _, err := f.Fetch(context.Background(), "vp", srv.URL); !errors.As(err, &be) {
		t.Errorf("should still be backing off after 90s, got %v", err)
	}
	clk.advance(31 * time.Second)
	if _, err := f.Fetch(context.Background(), "vp", srv.URL); err != nil {
		t.Errorf("should recover after Retry-After: %v", err)
	}
}

func TestBackoffGrows(t *testing.T) {
	f := &Fetcher{now: func() time.Time { return time.Time{} }}
	var waits []time.Duration
	for i := 0; i < 8; i++ {
		f.fail(0)
		waits = append(waits, f.retryAt.Sub(time.Time{}))
	}
	want := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute,
		8 * time.Minute, 15 * time.Minute, 15 * time.Minute, 15 * time.Minute}
	for i := range want {
		if waits[i] != want[i] {
			t.Errorf("failure %d: wait %v, want %v", i+1, waits[i], want[i])
		}
	}
}

func TestUnauthorized(t *testing.T) {
	var status atomic.Int32
	srv := feedServer(t, &status)
	defer srv.Close()
	f := &Fetcher{Auth: Auth{Key: "wrong", Header: "apikey"}}
	if _, err := f.Fetch(context.Background(), "vp", srv.URL); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("got %v", err)
	}
}

func TestKeyNeverInErrors(t *testing.T) {
	// A connection failure produces a *url.Error, whose text normally
	// includes the full URL with the key in the query string.
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := srv.URL
	srv.Close()
	f := &Fetcher{Auth: Auth{Key: secret, Param: "key"}}
	_, err := f.Fetch(context.Background(), "trip updates", addr+"/feed")
	if err == nil {
		t.Fatal("expected a connection error")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), addr) {
		t.Errorf("error leaks the URL or key: %v", err)
	}
	if !strings.HasPrefix(err.Error(), "trip updates:") {
		t.Errorf("error should name the feed: %v", err)
	}
}

func TestNonProtobufBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>Maintenance</html>"))
	}))
	defer srv.Close()
	f := &Fetcher{Auth: Auth{Key: secret, Header: "apikey"}}
	if _, err := f.Fetch(context.Background(), "vp", srv.URL); err == nil || !strings.Contains(err.Error(), "decoding") {
		t.Errorf("got %v", err)
	}
}

func TestBudget(t *testing.T) {
	loc, _ := time.LoadLocation("America/Montreal")
	path := filepath.Join(t.TempDir(), "budget.json")
	clk := &fakeClock{t: time.Date(2026, 10, 14, 23, 0, 0, 0, loc)}

	b := NewBudget(path, 100, loc) // stops at 95
	b.now = clk.now
	for i := 0; i < 95; i++ {
		if err := b.Take(); err != nil {
			t.Fatalf("request %d refused: %v", i+1, err)
		}
	}
	if err := b.Take(); !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("request 96: want ErrBudgetExhausted, got %v", err)
	}

	// Restarting the service keeps today's count.
	b2 := NewBudget(path, 100, loc)
	b2.now = clk.now
	if err := b2.Take(); !errors.Is(err, ErrBudgetExhausted) {
		t.Errorf("after restart: want ErrBudgetExhausted, got %v", err)
	}

	// Midnight in Montréal (not UTC) starts a new day.
	clk.t = time.Date(2026, 10, 15, 0, 0, 1, 0, loc)
	if err := b2.Take(); err != nil {
		t.Errorf("new day: %v", err)
	}
	if n, stop := b2.Used(); n != 1 || stop != 95 {
		t.Errorf("used %d/%d", n, stop)
	}
}

func TestBudgetUnlimited(t *testing.T) {
	b := NewBudget("", 0, time.UTC)
	for i := 0; i < 1000; i++ {
		if err := b.Take(); err != nil {
			t.Fatal(err)
		}
	}
}
