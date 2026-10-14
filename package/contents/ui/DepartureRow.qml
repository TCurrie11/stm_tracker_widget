/*
    stm_tracker_widget: live STM bus and métro departures for KDE Plasma 6
    https://github.com/TCurrie11/stm_tracker_widget

    File:    package/contents/ui/DepartureRow.qml
    Purpose: One route and direction with its next departures.
    Author:  TCurrie11 (https://github.com/TCurrie11)
    Version: 0.1.0
    Created: 2026-10-14
    Updated: 2026-10-14
    License: GPL-2.0-or-later

    SPDX-FileCopyrightText: 2026 TCurrie11
    SPDX-License-Identifier: GPL-2.0-or-later
*/

pragma ComponentBehavior: Bound

// One route in one direction: badge and headsign, then the next few
// departures. When narrow, the departures wrap under the headsign.

import QtQuick
import QtQuick.Layouts

import org.kde.plasma.components as PlasmaComponents
import org.kde.kirigami as Kirigami

GridLayout {
    id: row

    required property var app
    required property var group
    property bool wide: true

    readonly property var departures: app.visibleDepartures(group)
    // Cells show only buses that will stop here, never more than the
    // configured number, so a long detour can't push the row off the edge.
    readonly property var catchable: departures.filter(departure => !isGone(departure)).slice(0, Math.max(1, app.perDirection))
    // Canceled or detoured runs before the last bus shown, summarised in
    // one line instead of a cell each.
    readonly property var missed: {
        const last = catchable.length > 0 ? catchable[catchable.length - 1].expected : null;
        return departures.filter(departure => isGone(departure) && (last === null || departure.expected < last));
    }
    readonly property string missedText: {
        const canceled = missed.filter(departure => departure.status === "canceled");
        const skipped = missed.filter(departure => departure.status === "skipped");
        let parts = [];
        if (skipped.length === 1) {
            parts.push(i18n("%1 not stopping here (detour)", app.formatClock(skipped[0].scheduled)));
        } else if (skipped.length > 1) {
            parts.push(i18np("%1 bus not stopping here (detour)", "%1 buses not stopping here (detour)", skipped.length));
        }
        if (canceled.length === 1) {
            parts.push(i18n("%1 canceled", app.formatClock(canceled[0].scheduled)));
        } else if (canceled.length > 1) {
            parts.push(i18np("%1 canceled", "%1 canceled", canceled.length));
        }
        return parts.join(" · ");
    }
    readonly property real headsignWidth: Kirigami.Units.gridUnit * 9
    // Same width in every row so the columns line up; shrinks when narrow.
    readonly property real cellWidth: {
        const columns = Math.max(1, app.perDirection);
        const room = (wide ? width - headsignWidth - columnSpacing : width) - Kirigami.Units.largeSpacing * (columns - 1);
        return Math.max(Kirigami.Units.gridUnit * 3, Math.min(Kirigami.Units.gridUnit * 5, room / columns));
    }

    columns: wide ? 2 : 1
    columnSpacing: Kirigami.Units.largeSpacing
    rowSpacing: Kirigami.Units.smallSpacing

    RowLayout {
        spacing: Kirigami.Units.smallSpacing
        Layout.preferredWidth: row.wide ? row.headsignWidth : -1
        Layout.fillWidth: !row.wide
        Layout.alignment: Qt.AlignTop

        RouteBadge {
            route: row.group.route
            Layout.alignment: Qt.AlignTop
        }
        PlasmaComponents.Label {
            text: row.group.headsign
            elide: Text.ElideRight
            maximumLineCount: 2
            wrapMode: Text.Wrap
            Layout.fillWidth: true
        }
    }

    ColumnLayout {
        spacing: Kirigami.Units.smallSpacing
        Layout.fillWidth: true

        RowLayout {
            visible: row.catchable.length > 0
            spacing: Kirigami.Units.largeSpacing
            Layout.fillWidth: true

            Repeater {
                model: row.catchable
                delegate: departureCell
            }
            Item {
                Layout.fillWidth: true
            }
        }
        PlasmaComponents.Label {
            visible: row.missedText !== ""
            text: row.missedText
            font: Kirigami.Theme.smallFont
            color: Kirigami.Theme.neutralTextColor
            wrapMode: Text.Wrap
            Layout.fillWidth: true
        }
    }

    function isGone(departure) {
        return departure.status === "canceled" || departure.status === "skipped";
    }

    Component {
        id: departureCell

        ColumnLayout {
            id: cell

            required property var modelData
            readonly property var departure: modelData

            spacing: 0
            Layout.alignment: Qt.AlignTop
            Layout.preferredWidth: row.cellWidth
            Layout.maximumWidth: row.cellWidth
            opacity: row.app.statusOpacity(departure.status)

            RowLayout {
                spacing: Kirigami.Units.smallSpacing
                PlasmaComponents.Label {
                    text: row.app.countdown(cell.departure)
                    font.pointSize: Kirigami.Theme.defaultFont.pointSize * 1.4
                    font.bold: true
                    color: row.app.statusColor(cell.departure.status)
                }
                OccupancyBar {
                    level: cell.departure.occupancy
                    visible: row.app.showOccupancy && cell.departure.occupancy > 0
                    Layout.alignment: Qt.AlignVCenter
                }
            }
            PlasmaComponents.Label {
                text: row.app.delayText(cell.departure)
                font: Kirigami.Theme.smallFont
                color: row.app.statusColor(cell.departure.status)
                elide: Text.ElideRight
                Layout.fillWidth: true
            }
            PlasmaComponents.Label {
                visible: text !== ""
                text: row.app.stopsText(cell.departure)
                font: Kirigami.Theme.smallFont
                opacity: 0.7
                elide: Text.ElideRight
                Layout.fillWidth: true
            }
        }
    }
}
