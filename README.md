# stm_tracker_widget

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
- [x] Real-time delays, stops away, occupancy, cancellations and detours,
      decoded with a small hand-written protobuf reader (no dependencies)
- [x] Request budget (stops before STM's daily limit), backoff on errors,
      API key kept out of logs
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
./transitd live -stop <ID> -routes 24     # next departures with live data
```

## Configuration

Optional: copy `examples/config.json` to `~/.config/stm-transit/config.json`.
Without it, STM is used with the defaults.

The API key (free, from the [STM developer portal](https://portail.developpeurs.stm.info/apihub/#/login))
is read from the `STM_API_KEY` environment variable, never from the repo.
Keep it in `~/.config/stm-transit/secrets.env` (mode 600):

```sh
STM_API_KEY=your-key-here
```

and load it with `set -a; source ~/.config/stm-transit/secrets.env; set +a`.

Other agencies are added as extra entries in `agencies`.

`go test ./...` runs the tests against a small built-in schedule and real
STM realtime feeds captured on 2026-10-14 (`internal/realtime/testdata`).

## How live times are worked out

STM's trip updates give a predicted time for each stop still ahead of each
bus; delay is predicted minus scheduled. Vehicle positions give the stop each
bus is heading to and how full it is (three levels: seats free, few seats,
standing room). Two details of STM's feed that the code relies on:

- skipped stops (detours) are listed *after* all the other stops of a trip,
  out of sequence order, so nothing assumes the list is sorted;
- the metro has no realtime data, so metro times are always "scheduled".

Data © Société de transport de Montréal, CC BY 4.0.
