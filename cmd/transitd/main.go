// stm_tracker_widget: live STM bus and métro departures for KDE Plasma 6
// https://github.com/TCurrie11/stm_tracker_widget
//
// File:    cmd/transitd/main.go
// Purpose: transitd command line: the background service and terminal tools.
// Author:  TCurrie11 (https://github.com/TCurrie11)
// Version: 0.1.0
// Created: 2026-10-14
// Updated: 2026-10-14
// License: GPL-2.0-or-later

// Command transitd is the local backend for the STM transit widget.
//
//	transitd serve                      run the background service the widget talks to
//	transitd version                    print the version
//
// The other commands explore the schedule and live data from a terminal:
//
//	transitd fetch                      download STM's schedule data
//	transitd search berri               find stop / station IDs by name or code
//	transitd next -stop 52345 -routes 24 -n 3
//	transitd nearby "1500 rue Berri"    stops near an address
//	transitd plan -stop 52345 -routes 24  would the service poll right now?
//	transitd live -stop 52345 -routes 24  next departures with live data
//
// Settings come from ~/.config/stm-transit/config.json (optional; STM is
// the built-in default) and the STM_API_KEY environment variable.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/TCurrie11/stm_tracker_widget/internal/arrivals"
	"github.com/TCurrie11/stm_tracker_widget/internal/config"
	"github.com/TCurrie11/stm_tracker_widget/internal/geo"
	"github.com/TCurrie11/stm_tracker_widget/internal/gtfs"
	"github.com/TCurrie11/stm_tracker_widget/internal/poll"
	"github.com/TCurrie11/stm_tracker_widget/internal/realtime"
	"github.com/TCurrie11/stm_tracker_widget/internal/server"
)

// version is the release of transitd and the widget, kept in step with
// package/metadata.json. Shown by `transitd version`.
const version = "0.1.0"

// cfg is loaded once in main; the CLI uses the first agency.
var cfg config.Config

func agency() config.Agency { return cfg.Agencies[0] }

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	if os.Args[1] == "version" {
		fmt.Println("transitd", version)
		return
	}
	var err error
	if cfg, err = config.Load(config.DefaultPath()); err != nil {
		fmt.Fprintln(os.Stderr, "transitd: config:", err)
		os.Exit(1)
	}
	switch os.Args[1] {
	case "serve":
		err = cmdServe(os.Args[2:])
	case "fetch":
		err = cmdFetch(os.Args[2:])
	case "search":
		err = cmdSearch(os.Args[2:])
	case "next":
		err = cmdNext(os.Args[2:])
	case "nearby":
		err = cmdNearby(os.Args[2:])
	case "plan":
		err = cmdPlan(os.Args[2:])
	case "live":
		err = cmdLive(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "transitd:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  transitd serve  [-gtfs FILE] [-listen 127.0.0.1:8765]
  transitd version
  transitd fetch [-url URL]
  transitd search [-gtfs FILE] <name or stop code>
  transitd next   [-gtfs FILE] -stop ID [-routes 24,165] [-n 3] [-at 2026-10-12T08:00]
  transitd nearby [-gtfs FILE] [-radius 500] <address>
  transitd plan   [-gtfs FILE] -stop ID [-routes 24] [-at 2026-10-12T23:30]
  transitd live   [-gtfs FILE] -stop ID [-routes 24] [-n 3] [-tu FILE -vp FILE -at TIME]`)
	os.Exit(2)
}

// defaultGTFSPath is ~/.cache/stm-transit/<agency>.zip (respects XDG_CACHE_HOME).
func defaultGTFSPath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "stm-transit", agency().ID+".zip")
}

func cmdFetch(args []string) error {
	flags := flag.NewFlagSet("fetch", flag.ExitOnError)
	url := flags.String("url", agency().GTFSURL, "GTFS zip URL")
	output := flags.String("o", defaultGTFSPath(), "where to save it")
	flags.Parse(args)

	size, err := gtfs.Download(context.Background(), *url, *output)
	if err != nil {
		return err
	}
	fmt.Printf("saved %.1f MB to %s\n", float64(size)/1e6, *output)
	return nil
}

// gtfsMaxAge is when the service re-downloads the schedule. STM publishes
// a new zip every few weeks; a weekly check keeps up without being wasteful.
const gtfsMaxAge = 7 * 24 * time.Hour

func cmdServe(args []string) error {
	flags := flag.NewFlagSet("serve", flag.ExitOnError)
	path := flags.String("gtfs", defaultGTFSPath(), "GTFS zip (downloaded if missing)")
	listen := flags.String("listen", cfg.Listen, "address to listen on (loopback only)")
	flags.Parse(args)
	if err := checkLoopback(*listen); err != nil {
		return err
	}

	selected := agency()
	loc, err := time.LoadLocation(selected.Timezone)
	if err != nil {
		return err
	}
	logger := log.New(os.Stderr, "", 0) // journald adds timestamps
	if isTerminal() {
		logger.SetFlags(log.LstdFlags)
	}
	service := &server.Service{
		Agency:       selected,
		PollInterval: cfg.PollInterval.D(),
		WakeLead:     cfg.WakeLead.D(),
		IdleAfter:    cfg.IdleAfter.D(),
		GTFSPath:     *path,
		GTFSMaxAge:   gtfsMaxAge,
		Geocoder:     newGeocoder(),
		Log:          logger,
	}
	if selected.TripUpdatesURL != "" {
		service.Fetcher = newFetcher(loc)
		if selected.Key() == "" {
			logger.Printf("no API key (set %s): serving schedule times only", selected.APIKeyEnv)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Handler:           service.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go service.Run(ctx)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		httpServer.Shutdown(shutdownCtx)
	}()
	logger.Printf("listening on http://%s", listener.Addr())
	if err := httpServer.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	logger.Printf("stopped")
	return nil
}

// checkLoopback refuses to listen beyond this machine: the API has no
// authentication and the service spends the user's API quota.
func checkLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("listen address %q: %w", addr, err)
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("listen address %q: only loopback addresses (127.0.0.1, ::1, localhost) are allowed", addr)
	}
	return nil
}

func loadFeed(path string) (*gtfs.Feed, error) {
	timeZone, err := time.LoadLocation(agency().Timezone)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	feed, err := gtfs.Load(path, timeZone)
	if err != nil {
		return nil, fmt.Errorf("%w (run `transitd fetch` first?)", err)
	}
	fmt.Fprintf(os.Stderr, "loaded %d stops, %d routes in %s\n",
		len(feed.Stops), len(feed.Routes), time.Since(start).Round(time.Millisecond))
	return feed, nil
}

func cmdSearch(args []string) error {
	flags := flag.NewFlagSet("search", flag.ExitOnError)
	path := flags.String("gtfs", defaultGTFSPath(), "GTFS zip")
	flags.Parse(args)
	if flags.NArg() == 0 {
		usage()
	}
	feed, err := loadFeed(*path)
	if err != nil {
		return err
	}
	for _, stop := range feed.SearchStops(strings.Join(flags.Args(), " "), 25) {
		kind := "stop"
		if stop.IsStation {
			kind = "station"
		}
		fmt.Printf("%-8s %-12s code %-6s %s\n", kind, stop.ID, stop.Code, stop.Name)
	}
	return nil
}

func cmdNext(args []string) error {
	flags := flag.NewFlagSet("next", flag.ExitOnError)
	path := flags.String("gtfs", defaultGTFSPath(), "GTFS zip")
	stop := flags.String("stop", "", "stop or station ID (see `transitd search`)")
	routes := flags.String("routes", "", "comma-separated route numbers, empty = all")
	perDirection := flags.Int("n", 3, "departures per direction")
	at := flags.String("at", "", "pretend it is this local time (2006-01-02T15:04)")
	flags.Parse(args)
	if *stop == "" {
		usage()
	}
	feed, err := loadFeed(*path)
	if err != nil {
		return err
	}
	if _, ok := feed.Stops[*stop]; !ok {
		return fmt.Errorf("no stop with ID %q", *stop)
	}
	now, err := parseAt(*at, feed)
	if err != nil {
		return err
	}
	filter := splitRoutes(*routes)

	fmt.Printf("%s — %s\n", feed.Stops[*stop].Name, now.In(feed.TimeZone).Format("Mon 15:04"))
	groups := feed.Next(*stop, now, *perDirection, filter)
	if len(groups) == 0 {
		fmt.Println("  no departures in the next 12 hours")
	}
	for _, group := range groups {
		fmt.Printf("  %-4s → %-30s", group.Route.ShortName, group.Headsign)
		for _, departure := range group.Departures {
			minutes := int(departure.Time.Sub(now).Minutes())
			fmt.Printf("  %s (%d min)", departure.Time.Format("15:04"), minutes)
		}
		if len(group.Departures) > 0 && group.Departures[0].FromFrequency {
			fmt.Print("  [scheduled frequency]")
		}
		fmt.Println()
	}
	return nil
}

func parseAt(at string, f *gtfs.Feed) (time.Time, error) {
	if at == "" {
		return time.Now(), nil
	}
	return time.ParseInLocation("2006-01-02T15:04", at, f.TimeZone)
}

func splitRoutes(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

func newGeocoder() geo.Geocoder {
	if cfg.Geocoder.Provider == "google" {
		return &geo.Google{Key: cfg.Geocoder.GoogleKey, Bias: geo.Montreal}
	}
	return &geo.Nominatim{Contact: cfg.Geocoder.Contact, Bias: geo.Montreal}
}

func cmdNearby(args []string) error {
	flags := flag.NewFlagSet("nearby", flag.ExitOnError)
	path := flags.String("gtfs", defaultGTFSPath(), "GTFS zip")
	radius := flags.Float64("radius", 500, "search radius in meters")
	flags.Parse(args)
	if flags.NArg() == 0 {
		usage()
	}
	feed, err := loadFeed(*path)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	place, err := newGeocoder().Geocode(ctx, strings.Join(flags.Args(), " "))
	if err != nil {
		return err
	}
	fmt.Printf("%s (%.5f, %.5f)\n", place.Label, place.Lat, place.Lon)
	for _, stop := range feed.NearbyStops(place.Lat, place.Lon, *radius, 20) {
		var names []string
		for _, route := range feed.RoutesAt(stop.ID) {
			names = append(names, route.ShortName)
		}
		fmt.Printf("  %4.0f m  %-12s %-40s routes %s\n", stop.Meters, stop.ID, stop.Name, strings.Join(names, " "))
	}
	return nil
}

func cmdPlan(args []string) error {
	flags := flag.NewFlagSet("plan", flag.ExitOnError)
	path := flags.String("gtfs", defaultGTFSPath(), "GTFS zip")
	stop := flags.String("stop", "", "stop or station ID")
	routes := flags.String("routes", "", "comma-separated route numbers, empty = all")
	at := flags.String("at", "", "pretend it is this local time (2006-01-02T15:04)")
	flags.Parse(args)
	if *stop == "" {
		usage()
	}
	feed, err := loadFeed(*path)
	if err != nil {
		return err
	}
	now, err := parseAt(*at, feed)
	if err != nil {
		return err
	}
	plan := poll.Decide(feed, []poll.Watch{{Stop: *stop, Routes: splitRoutes(*routes)}}, now, cfg.WakeLead.D())
	if plan.Active {
		fmt.Printf("polling every %s (%s)\n", cfg.PollInterval.D(), plan.Reason)
	} else {
		fmt.Printf("paused, %s; resume at %s\n", plan.Reason, plan.WakeAt.In(feed.TimeZone).Format("Mon 15:04"))
	}
	return nil
}

// newFetcher builds a realtime fetcher for the first agency, with the daily
// budget kept in ~/.cache/stm-transit/budget-<agency>.json.
func newFetcher(loc *time.Location) *realtime.Fetcher {
	selected := agency()
	return &realtime.Fetcher{
		Auth:   realtime.Auth{Key: selected.Key(), Header: selected.APIKeyHeader, Param: selected.APIKeyParam},
		Budget: realtime.NewBudget(filepath.Join(filepath.Dir(defaultGTFSPath()), "budget-"+selected.ID+".json"), selected.DailyRequestLimit, loc),
	}
}

// liveSnapshot fetches both feeds, or reads them from files (-tu/-vp) for
// offline testing. On failure it returns nil and a short reason: the board
// then shows schedule times, as the widget will.
func liveSnapshot(feed *gtfs.Feed, tripUpdatesFile, vehiclePositionsFile string) (*realtime.Snapshot, string) {
	if tripUpdatesFile != "" || vehiclePositionsFile != "" {
		var decoded [2]*realtime.Feed
		for i, path := range []string{tripUpdatesFile, vehiclePositionsFile} {
			if path == "" {
				continue
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, err.Error()
			}
			if decoded[i], err = realtime.Decode(data); err != nil {
				return nil, path + ": " + err.Error()
			}
		}
		return realtime.NewSnapshot(decoded[0], decoded[1]), ""
	}
	selected := agency()
	if selected.TripUpdatesURL == "" {
		return nil, "no realtime feed configured"
	}
	fetcher := newFetcher(feed.TimeZone)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tripUpdates, err := fetcher.Fetch(ctx, "trip updates", selected.TripUpdatesURL)
	if err != nil {
		return nil, err.Error()
	}
	var vehiclePositions *realtime.Feed
	if selected.VehiclePositionsURL != "" {
		// Positions only add stops-away and occupancy; predictions alone are
		// still worth showing if this one fails.
		if vehiclePositions, err = fetcher.Fetch(ctx, "vehicle positions", selected.VehiclePositionsURL); err != nil {
			fmt.Fprintln(os.Stderr, "note:", err)
		}
	}
	if used, stop := fetcher.Budget.Used(); stop > 0 {
		fmt.Fprintf(os.Stderr, "requests today: %d of %d\n", used, stop)
	}
	return realtime.NewSnapshot(tripUpdates, vehiclePositions), ""
}

func cmdLive(args []string) error {
	flags := flag.NewFlagSet("live", flag.ExitOnError)
	path := flags.String("gtfs", defaultGTFSPath(), "GTFS zip")
	stop := flags.String("stop", "", "stop or station ID")
	routes := flags.String("routes", "", "comma-separated route numbers, empty = all")
	perDirection := flags.Int("n", 3, "departures per direction")
	tripUpdatesFile := flags.String("tu", "", "read trip updates from this file instead of fetching")
	vehiclePositionsFile := flags.String("vp", "", "read vehicle positions from this file instead of fetching")
	at := flags.String("at", "", "pretend it is this local time (with -tu/-vp)")
	flags.Parse(args)
	if *stop == "" {
		usage()
	}
	feed, err := loadFeed(*path)
	if err != nil {
		return err
	}
	if _, ok := feed.Stops[*stop]; !ok {
		return fmt.Errorf("no stop with ID %q", *stop)
	}
	now, err := parseAt(*at, feed)
	if err != nil {
		return err
	}
	snapshot, problem := liveSnapshot(feed, *tripUpdatesFile, *vehiclePositionsFile)

	fmt.Printf("%s — %s\n", feed.Stops[*stop].Name, now.In(feed.TimeZone).Format("Mon 15:04"))
	if problem != "" {
		fmt.Printf("  Live data paused — showing schedule (%s)\n", problem)
	} else if !snapshot.FeedTime.IsZero() {
		fmt.Printf("  live data from %s\n", snapshot.FeedTime.In(feed.TimeZone).Format("15:04:05"))
	}
	groups := arrivals.Board(feed, snapshot, *stop, splitRoutes(*routes), now, *perDirection, realtime.DefaultThresholds)
	if len(groups) == 0 {
		fmt.Println("  no departures in the next 12 hours")
	}
	color := isTerminal()
	for _, group := range groups {
		fmt.Printf("\n  %s → %s\n", group.Route.ShortName, group.Headsign)
		for _, departure := range group.Departures {
			fmt.Println("    " + formatDeparture(departure, now, feed.TimeZone, color))
		}
	}
	return nil
}

func formatDeparture(departure arrivals.Departure, now time.Time, loc *time.Location, color bool) string {
	minutes := int(departure.Expected.Sub(now).Minutes())
	line := fmt.Sprintf("%s  in %2d min", departure.Expected.In(loc).Format("15:04"), minutes)
	paint := ""
	switch departure.Status {
	case realtime.Late:
		line += fmt.Sprintf("  +%d late", int(departure.Delay.Round(time.Minute).Minutes()))
		paint = "\x1b[31m" // red
	case realtime.Early:
		line += fmt.Sprintf("  %d early", int(departure.Delay.Round(time.Minute).Minutes()))
		paint = "\x1b[33m" // orange/yellow
	case realtime.OnTime:
		line += "  on time"
	case realtime.Scheduled:
		line += "  scheduled"
		paint = "\x1b[2m" // dim
	case realtime.Canceled:
		line = departure.Scheduled.In(loc).Format("15:04") + "  canceled"
		paint = "\x1b[9;2m" // struck through
	case realtime.Skipped:
		line = departure.Scheduled.In(loc).Format("15:04") + "  not stopping here (detour)"
		paint = "\x1b[9;2m"
	}
	switch {
	case departure.StopsAway == 0:
		line += "  · at the stop"
	case departure.StopsAway == 1:
		line += "  · next stop"
	case departure.StopsAway > 1:
		line += fmt.Sprintf("  · %d stops away", departure.StopsAway)
	}
	if level := departure.Occupancy.Level(); level > 0 {
		bars := strings.Repeat("▮", level) + strings.Repeat("▯", 3-level)
		label := [...]string{"", "seats free", "few seats", "standing room"}[level]
		line += "  " + bars + " " + label
	}
	if color && paint != "" {
		return paint + line + "\x1b[0m"
	}
	return line
}

func isTerminal() bool {
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
