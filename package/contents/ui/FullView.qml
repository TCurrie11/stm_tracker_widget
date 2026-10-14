pragma ComponentBehavior: Bound

/*
    SPDX-FileCopyrightText: 2026 Thomas Currie
    SPDX-License-Identifier: GPL-2.0-or-later
*/

// Popup / desktop view: a section per configured stop, in order.

import QtQuick
import QtQuick.Layouts

import org.kde.plasma.components as PlasmaComponents
import org.kde.plasma.extras as PlasmaExtras
import org.kde.kirigami as Kirigami

PlasmaExtras.Representation {
    id: full

    required property var app

    readonly property bool wide: width >= Kirigami.Units.gridUnit * 20
    // Live data is missing for a reason worth mentioning (not just "it's
    // night" or "starting up").
    readonly property var pausedReasons: ["no_key", "unauthorized", "budget", "error"]

    Layout.minimumWidth: Kirigami.Units.gridUnit * 14
    Layout.preferredWidth: Kirigami.Units.gridUnit * 24
    Layout.minimumHeight: Kirigami.Units.gridUnit * 10
    Layout.preferredHeight: Kirigami.Units.gridUnit * 22

    collapseMarginsHint: true

    PlasmaExtras.PlaceholderMessage {
        anchors.centerIn: parent
        width: parent.width - Kirigami.Units.gridUnit * 4
        visible: full.app.watches.length === 0
        iconName: "mark-location"
        text: i18n("Add a stop in settings")
        helpfulAction: Kirigami.Action {
            icon.name: "configure"
            text: i18n("Settings…")
            onTriggered: full.app.openSettings()
        }
    }

    PlasmaExtras.PlaceholderMessage {
        anchors.centerIn: parent
        width: parent.width - Kirigami.Units.gridUnit * 4
        visible: full.app.watches.length > 0 && full.app.unavailable
        iconName: "network-disconnect"
        text: i18n("Transit times unavailable — retrying")
    }

    PlasmaComponents.BusyIndicator {
        anchors.centerIn: parent
        visible: full.app.watches.length > 0 && !full.app.hasData && !full.app.unavailable
    }

    ColumnLayout {
        anchors.fill: parent
        visible: full.app.hasData && !full.app.unavailable
        spacing: Kirigami.Units.smallSpacing

        PlasmaComponents.ScrollView {
            id: scroll
            Layout.fillWidth: true
            Layout.fillHeight: true
            contentWidth: availableWidth
            PlasmaComponents.ScrollBar.horizontal.policy: PlasmaComponents.ScrollBar.AlwaysOff

            ColumnLayout {
                width: scroll.availableWidth
                spacing: Kirigami.Units.largeSpacing
                opacity: full.app.reachable ? 1 : 0.6

                Repeater {
                    model: full.app.boards
                    delegate: section
                }
            }
        }

        PlasmaComponents.Label {
            Layout.fillWidth: true
            font: Kirigami.Theme.smallFont
            opacity: 0.6
            wrapMode: Text.Wrap
            text: {
                const credits = full.app.boards.length > 0 && full.app.boards[0].data ? full.app.boards[0].data.credits : "";
                const u = full.app.lastUpdated;
                let when = "";
                if (u) {
                    const ago = Math.floor((full.app.now - u) / 60000);
                    when = full.app.reachable || ago < 1
                        ? i18n("Updated %1", Qt.formatTime(u, Qt.locale().timeFormat(Locale.ShortFormat)))
                        : i18np("Updated %1 min ago", "Updated %1 min ago", ago);
                }
                return [when, credits].filter(s => s !== "").join(" · ");
            }
        }
    }

    Component {
        id: section

        ColumnLayout {
            id: sec

            required property var modelData
            readonly property var watch: modelData.watch
            readonly property var board: modelData.data
            readonly property var groups: board ? board.routes.filter(g => full.app.visibleDepartures(g).length > 0) : []
            // Earliest departure, for "next at 5:30" when it is tomorrow.
            readonly property var first: {
                let best = null;
                for (const g of groups) {
                    for (const d of g.departures) {
                        if (!best || d.expected < best.expected) {
                            best = d;
                        }
                    }
                }
                return best;
            }
            // Service is over for the night: nothing within the hour, and the
            // next departure is tomorrow (or it is already past midnight).
            readonly property bool tomorrowOnly: first !== null
                && new Date(first.expected) - full.app.now > 3600000
                && (new Date(first.expected).toDateString() !== full.app.now.toDateString() || full.app.now.getHours() < 4)

            Layout.fillWidth: true
            spacing: Kirigami.Units.smallSpacing

            Kirigami.Heading {
                level: 4
                text: sec.watch.stopName || (sec.board ? sec.board.stop.name : sec.watch.stop)
                elide: Text.ElideRight
                Layout.fillWidth: true
            }

            PlasmaComponents.Label {
                visible: sec.board !== null && sec.board.live.state !== "live" && full.pausedReasons.includes(sec.board.live.reason)
                text: i18n("Live data paused — showing schedule")
                font: Kirigami.Theme.smallFont
                opacity: 0.7
                Layout.fillWidth: true
                wrapMode: Text.Wrap
            }

            PlasmaComponents.Label {
                visible: sec.board !== null && (sec.groups.length === 0 || sec.tomorrowOnly)
                text: sec.first
                    ? i18n("No more departures tonight — next at %1", full.app.formatClock(sec.first.expected))
                    : i18n("No departures in the next 12 hours")
                opacity: 0.7
                Layout.fillWidth: true
                wrapMode: Text.Wrap
            }

            PlasmaComponents.Label {
                visible: sec.board === null
                text: i18n("Loading…")
                opacity: 0.7
            }

            Repeater {
                model: sec.tomorrowOnly ? [] : sec.groups
                delegate: DepartureRow {
                    required property var modelData
                    app: full.app
                    group: modelData
                    wide: full.wide
                    Layout.fillWidth: true
                }
            }
        }
    }
}
