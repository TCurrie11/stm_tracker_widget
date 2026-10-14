# stm-transit

A KDE Plasma 6 widget showing the next departures for Montréal STM buses and
metro, with live delays and bus occupancy.

Two parts:

- **`transitd`** — a small Go service that reads STM's schedule (GTFS) and
  real-time feeds (GTFS-Realtime) and serves simple JSON on localhost.
- **the plasmoid** — QML that displays that JSON. *(coming)*

## Status

- [x] Static schedule loader: next departures per route and direction,
      metro frequencies, after-midnight trips, holiday exceptions
- [x] Per-agency config (any GTFS agency), pluggable geocoder
      (OpenStreetMap by default, Google optional), nearby-stop search
- [x] Schedule-aware polling: pauses after the last departure on watched
      routes and resumes 30 min before the first one
- [ ] Real-time delays, stops away, occupancy
- [ ] HTTP API and nearby-stop search
- [ ] Plasmoid

## Try it

```sh
go build -o transitd ./cmd/transitd
./transitd fetch                          # downloads STM's schedule data
./transitd search berri                   # find stop/station IDs
./transitd next -stop <ID> -routes 24 -n 3
./transitd nearby "1500 rue Berri"        # stops near an address
./transitd plan -stop <ID> -routes 24     # would it poll right now?
```

## Configuration

Optional: copy `examples/config.json` to `~/.config/stm-transit/config.json`.
Without it, STM is used with the defaults. Keep the API key out of the file
by exporting it instead: `export STM_API_KEY=...`.

Other agencies are added as extra entries in `agencies`.

`go test ./...` runs the tests against a small built-in feed.

Data © Société de transport de Montréal, CC BY 4.0.
