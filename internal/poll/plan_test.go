package poll

import (
	"testing"
	"time"

	"github.com/TCurrie11/stm_tracker_widget/internal/gtfs"
	"github.com/TCurrie11/stm_tracker_widget/internal/gtfs/gtfstest"
)

func feed(t *testing.T) *gtfs.Feed {
	t.Helper()
	loc, _ := time.LoadLocation("America/Montreal")
	f, err := gtfs.LoadBytes(gtfstest.Zip(), loc)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func at(f *gtfs.Feed, s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, f.Loc)
	if err != nil {
		panic(err)
	}
	return t
}

const lead = 30 * time.Minute

func TestDecide(t *testing.T) {
	f := feed(t)
	bus := []Watch{{Stop: "52345", Routes: []string{"24"}}}
	busOuest := []Watch{{Stop: "52346"}}
	metro := []Watch{{Stop: "STATION-BERRI"}}

	cases := []struct {
		name    string
		watches []Watch
		now     string
		active  bool
		wake    string // expected WakeAt when not active
	}{
		{"bus due soon", bus, "2026-10-12 08:01", true, ""},
		{"evening: sleep until 30 min before the 01:10 night trip", bus, "2026-10-12 20:00", false, "2026-10-13 00:40"},
		{"just before wake time", bus, "2026-10-13 00:39", false, "2026-10-13 00:40"},
		{"at wake time", bus, "2026-10-13 00:40", true, ""},
		{"late last bus still tracked within grace", busOuest, "2026-10-12 08:20", true, ""},
		{"after grace, nothing within lookahead: recheck in an hour", busOuest, "2026-10-12 08:30", false, "2026-10-12 09:30"},
		{"metro before opening", metro, "2026-10-12 04:00", false, "2026-10-12 05:00"},
		{"metro opening", metro, "2026-10-12 05:05", true, ""},
		{"nothing watched", nil, "2026-10-12 08:00", false, "2026-10-12 09:00"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := Decide(f, c.watches, at(f, c.now), lead)
			if p.Active != c.active {
				t.Fatalf("active = %v, want %v (%s)", p.Active, c.active, p.Reason)
			}
			if !c.active && !p.WakeAt.Equal(at(f, c.wake)) {
				t.Errorf("wake = %s, want %s (%s)", p.WakeAt.In(f.Loc).Format("2006-01-02 15:04"), c.wake, p.Reason)
			}
		})
	}
}

func TestDecideReason(t *testing.T) {
	f := feed(t)
	p := Decide(f, []Watch{{Stop: "STATION-BERRI"}}, at(f, "2026-10-12 04:00"), lead)
	if p.Reason != "no service until 05:30" {
		t.Errorf("reason = %q", p.Reason)
	}
}
