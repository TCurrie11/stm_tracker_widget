/*
    stm_tracker_widget: live STM bus and métro departures for KDE Plasma 6
    https://github.com/TCurrie11/stm_tracker_widget

    File:    package/contents/ui/main.qml
    Purpose: Widget root: fetching, countdown and shared formatting.
    Author:  TCurrie11 (https://github.com/TCurrie11)
    Version: 0.1.0
    Created: 2026-10-14
    Updated: 2026-10-14
    License: GPL-2.0-or-later

    SPDX-FileCopyrightText: 2026 TCurrie11
    SPDX-License-Identifier: GPL-2.0-or-later
*/

// The widget only displays what transitd serves; all transit logic lives
// in the Go service. Every refresh sends the configured stops and routes,
// which is also how transitd knows the widget is open and worth polling for.

import QtQuick

import org.kde.plasma.plasmoid
import org.kde.plasma.core as PlasmaCore
import org.kde.kirigami as Kirigami

import "api.js" as Api

PlasmoidItem {
    id: root

    readonly property var watches: Api.parseWatches(Plasmoid.configuration.watches)
    readonly property string serviceUrl: Plasmoid.configuration.serviceUrl
    readonly property int perDirection: Plasmoid.configuration.departuresPerDirection
    readonly property bool showOccupancy: Plasmoid.configuration.showOccupancy
    readonly property bool earlyGreen: Plasmoid.configuration.earlyColor === "green"

    // One entry per watch, in the same order:
    // {watch, data (last good response or null), ok (last request worked), updated (Date)}
    property var boards: []
    property date now: new Date()
    property int generation: 0 // drops replies to requests made before a config change

    // Oldest successful update across all boards; invalid until each has one.
    readonly property var lastUpdated: {
        let oldest = null;
        for (const board of boards) {
            if (!board.updated) {
                return null;
            }
            if (!oldest || board.updated < oldest) {
                oldest = board.updated;
            }
        }
        return oldest;
    }
    readonly property bool reachable: boards.every(board => board.ok !== false)
    readonly property bool hasData: boards.some(board => board.data !== null)
    // Spec: keep showing the last data, dimmed, for 15 minutes; then give up.
    readonly property bool unavailable: !reachable && (!hasData || !lastUpdated || now - lastUpdated > 15 * 60000)

    // The panel shows the next departure of the first watch's first route.
    readonly property var topDeparture: {
        const firstBoard = boards[0];
        if (!firstBoard || !firstBoard.data || firstBoard.data.routes.length === 0) {
            return null;
        }
        const firstWatch = firstBoard.watch;
        const wantedRoute = firstWatch.routes && firstWatch.routes.length > 0 ? firstWatch.routes[0] : firstBoard.data.routes[0].route.short_name;
        let best = null;
        for (const group of firstBoard.data.routes) {
            if (group.route.short_name !== wantedRoute && group.route.id !== wantedRoute) {
                continue;
            }
            for (const departure of visibleDepartures(group)) {
                if (departure.status === "canceled" || departure.status === "skipped") {
                    continue;
                }
                if (!best || new Date(departure.expected) < new Date(best.departure.expected)) {
                    best = { group: group, departure: departure };
                }
            }
        }
        return best;
    }

    // On the desktop, the background can be switched off from the widget's
    // edit-mode toolbar ("Show Background"); Plasma saves the choice.
    Plasmoid.backgroundHints: PlasmaCore.Types.DefaultBackground | PlasmaCore.Types.ConfigurableBackground

    switchWidth: Kirigami.Units.gridUnit * 12
    switchHeight: Kirigami.Units.gridUnit * 10

    toolTipMainText: topDeparture ? topDeparture.group.route.short_name + " → " + topDeparture.group.headsign : i18n("STM Transit")
    toolTipSubText: {
        if (!topDeparture) {
            return watches.length === 0 ? i18n("Add a stop in settings") : "";
        }
        const departure = topDeparture.departure;
        let lines = [];
        if (departure.realtime) {
            lines.push(i18n("Scheduled %1 · expected %2", formatClock(departure.scheduled), formatClock(departure.expected)));
        } else {
            lines.push(i18n("Scheduled %1", formatClock(departure.scheduled)));
        }
        const stops = stopsText(departure);
        if (stops !== "") {
            lines.push(stops);
        }
        return lines.join("\n");
    }

    compactRepresentation: CompactView {
        app: root
    }
    fullRepresentation: FullView {
        app: root
    }

    // ---- data ----

    function resetBoards() {
        boards = watches.map(watch => ({ watch: watch, data: null, ok: null, updated: null }));
    }

    function refresh() {
        const requestGeneration = ++generation;
        const watchesAtRequest = watches;
        watchesAtRequest.forEach((watch, index) => {
            Api.departures(serviceUrl, watch.stop, watch.routes, perDirection, (error, data) => {
                if (requestGeneration !== generation) {
                    return;
                }
                let updatedBoards = boards.slice();
                if (error) {
                    console.warn("stmtransit:", error);
                    updatedBoards[index] = Object.assign({}, updatedBoards[index], { ok: false });
                } else {
                    updatedBoards[index] = { watch: watch, data: data, ok: true, updated: new Date() };
                }
                boards = updatedBoards;
            });
        });
    }

    onWatchesChanged: {
        resetBoards();
        refresh();
    }
    onServiceUrlChanged: refresh()
    onPerDirectionChanged: refresh()

    Timer {
        interval: Math.max(30, Plasmoid.configuration.refreshSeconds) * 1000
        running: root.watches.length > 0
        repeat: true
        onTriggered: root.refresh()
    }

    // Countdowns move between fetches; this costs no requests.
    Timer {
        interval: 15000
        running: true
        repeat: true
        onTriggered: root.now = new Date()
    }

    Component.onCompleted: {
        resetBoards();
        refresh();
    }

    // ---- shared formatting ----

    // Departures still worth showing: drop any that left over 30 s ago.
    function visibleDepartures(group) {
        return group.departures.filter(departure => new Date(departure.expected) - now > -30000);
    }

    function formatClock(iso) {
        return Qt.formatTime(new Date(iso), Qt.locale().timeFormat(Locale.ShortFormat));
    }

    // "Now", "4 min", or the clock time when it is over an hour away.
    function countdown(departure) {
        if (departure.status === "canceled" || departure.status === "skipped") {
            return formatClock(departure.scheduled);
        }
        const minutes = Api.minutesUntil(departure.expected, now);
        if (minutes <= 0) {
            return i18nc("departure is due", "Now");
        }
        if (minutes > 60) {
            return formatClock(departure.expected);
        }
        return i18np("%1 min", "%1 min", minutes);
    }

    function delayText(departure) {
        const minutes = Math.round(Math.abs(departure.delay_seconds) / 60);
        switch (departure.status) {
        case "late":
            return "+" + minutes;
        case "early":
            return "−" + minutes;
        case "on_time":
            return i18n("on time");
        case "canceled":
            return i18n("Canceled");
        case "skipped":
            return i18n("Not stopping here (detour)");
        }
        return i18n("scheduled");
    }

    function stopsText(departure) {
        if (departure.stops_away === null || departure.stops_away === undefined) {
            return "";
        }
        if (departure.stops_away === 0) {
            return i18n("at stop");
        }
        if (departure.stops_away === 1) {
            return i18n("next stop");
        }
        return i18np("%1 stop", "%1 stops", departure.stops_away);
    }

    function statusColor(status) {
        switch (status) {
        case "late":
            return Kirigami.Theme.negativeTextColor;
        case "early":
            return earlyGreen ? Kirigami.Theme.positiveTextColor : Kirigami.Theme.neutralTextColor;
        case "canceled":
        case "skipped":
            return Kirigami.Theme.disabledTextColor;
        }
        return Kirigami.Theme.textColor;
    }

    function statusOpacity(status) {
        return status === "scheduled" ? 0.6 : 1;
    }

    function openSettings() {
        Plasmoid.internalAction("configure").trigger();
    }
}
