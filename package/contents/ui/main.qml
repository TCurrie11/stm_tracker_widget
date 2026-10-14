/*
    SPDX-FileCopyrightText: 2026 Thomas Currie
    SPDX-License-Identifier: GPL-2.0-or-later
*/

// The widget only displays what transitd serves; all transit logic lives
// in the Go service. Every refresh sends the configured stops and routes,
// which is also how transitd knows the widget is open and worth polling for.

import QtQuick

import org.kde.plasma.plasmoid
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
        for (const b of boards) {
            if (!b.updated) {
                return null;
            }
            if (!oldest || b.updated < oldest) {
                oldest = b.updated;
            }
        }
        return oldest;
    }
    readonly property bool reachable: boards.every(b => b.ok !== false)
    readonly property bool hasData: boards.some(b => b.data !== null)
    // Spec: keep showing the last data, dimmed, for 15 minutes; then give up.
    readonly property bool unavailable: !reachable && (!hasData || !lastUpdated || now - lastUpdated > 15 * 60000)

    // The panel shows the next departure of the first watch's first route.
    readonly property var topDeparture: {
        const b = boards[0];
        if (!b || !b.data || b.data.routes.length === 0) {
            return null;
        }
        const wanted = b.watch.routes && b.watch.routes.length > 0 ? b.watch.routes[0] : b.data.routes[0].route.short_name;
        let best = null;
        for (const g of b.data.routes) {
            if (g.route.short_name !== wanted && g.route.id !== wanted) {
                continue;
            }
            for (const d of visibleDepartures(g)) {
                if (d.status === "canceled" || d.status === "skipped") {
                    continue;
                }
                if (!best || new Date(d.expected) < new Date(best.departure.expected)) {
                    best = { group: g, departure: d };
                }
            }
        }
        return best;
    }

    switchWidth: Kirigami.Units.gridUnit * 12
    switchHeight: Kirigami.Units.gridUnit * 10

    toolTipMainText: topDeparture ? topDeparture.group.route.short_name + " → " + topDeparture.group.headsign : i18n("STM Transit")
    toolTipSubText: {
        if (!topDeparture) {
            return watches.length === 0 ? i18n("Add a stop in settings") : "";
        }
        const d = topDeparture.departure;
        let lines = [];
        if (d.realtime) {
            lines.push(i18n("Scheduled %1 · expected %2", formatClock(d.scheduled), formatClock(d.expected)));
        } else {
            lines.push(i18n("Scheduled %1", formatClock(d.scheduled)));
        }
        const s = stopsText(d);
        if (s !== "") {
            lines.push(s);
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
        boards = watches.map(w => ({ watch: w, data: null, ok: null, updated: null }));
    }

    function refresh() {
        const gen = ++generation;
        const ws = watches;
        ws.forEach((w, i) => {
            Api.departures(serviceUrl, w.stop, w.routes, perDirection, (err, data) => {
                if (gen !== generation) {
                    return;
                }
                let next = boards.slice();
                if (err) {
                    console.warn("stmtransit:", err);
                    next[i] = Object.assign({}, next[i], { ok: false });
                } else {
                    next[i] = { watch: w, data: data, ok: true, updated: new Date() };
                }
                boards = next;
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
        return group.departures.filter(d => new Date(d.expected) - now > -30000);
    }

    function formatClock(iso) {
        return Qt.formatTime(new Date(iso), Qt.locale().timeFormat(Locale.ShortFormat));
    }

    // "Now", "4 min", or the clock time when it is over an hour away.
    function countdown(d) {
        if (d.status === "canceled" || d.status === "skipped") {
            return formatClock(d.scheduled);
        }
        const m = Api.minutesUntil(d.expected, now);
        if (m <= 0) {
            return i18nc("departure is due", "Now");
        }
        if (m > 60) {
            return formatClock(d.expected);
        }
        return i18np("%1 min", "%1 min", m);
    }

    function delayText(d) {
        const mins = Math.round(Math.abs(d.delay_seconds) / 60);
        switch (d.status) {
        case "late":
            return "+" + mins;
        case "early":
            return "−" + mins;
        case "on_time":
            return i18n("on time");
        case "canceled":
            return i18n("Canceled");
        case "skipped":
            return i18n("Not stopping here (detour)");
        }
        return i18n("scheduled");
    }

    function stopsText(d) {
        if (d.stops_away === null || d.stops_away === undefined) {
            return "";
        }
        if (d.stops_away === 0) {
            return i18n("at stop");
        }
        if (d.stops_away === 1) {
            return i18n("next stop");
        }
        return i18np("%1 stop", "%1 stops", d.stops_away);
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
