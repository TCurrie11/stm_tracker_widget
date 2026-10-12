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
- [ ] Real-time delays, stops away, occupancy
- [ ] HTTP API and nearby-stop search
- [ ] Plasmoid

## Try it

```sh
go build -o transitd ./cmd/transitd
./transitd fetch                          # downloads STM's schedule data
./transitd search berri                   # find stop/station IDs
./transitd next -stop <ID> -routes 24 -n 3
```

`go test ./...` runs the tests against a small built-in feed.

Data © Société de transport de Montréal, CC BY 4.0.
