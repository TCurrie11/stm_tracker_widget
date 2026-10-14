pragma ComponentBehavior: Bound

/*
    SPDX-FileCopyrightText: 2026 Thomas Currie
    SPDX-License-Identifier: GPL-2.0-or-later
*/

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
    readonly property real headsignWidth: Kirigami.Units.gridUnit * 9
    // Same width in every row so the columns line up; shrinks when narrow.
    readonly property real cellWidth: {
        const n = Math.max(1, app.perDirection);
        const room = (wide ? width - headsignWidth - columnSpacing : width) - Kirigami.Units.largeSpacing * (n - 1);
        return Math.max(Kirigami.Units.gridUnit * 3, Math.min(Kirigami.Units.gridUnit * 5, room / n));
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

    RowLayout {
        spacing: Kirigami.Units.largeSpacing
        Layout.fillWidth: true

        Repeater {
            model: row.departures
            delegate: departureCell
        }
        Item {
            Layout.fillWidth: true
        }
    }

    Component {
        id: departureCell

        ColumnLayout {
            id: cell

            required property var modelData
            readonly property var d: modelData
            readonly property bool gone: d.status === "canceled" || d.status === "skipped"

            spacing: 0
            Layout.alignment: Qt.AlignTop
            Layout.preferredWidth: row.cellWidth
            Layout.maximumWidth: row.cellWidth
            opacity: row.app.statusOpacity(d.status)

            RowLayout {
                spacing: Kirigami.Units.smallSpacing
                PlasmaComponents.Label {
                    text: row.app.countdown(cell.d)
                    font.pointSize: Kirigami.Theme.defaultFont.pointSize * 1.4
                    font.bold: !cell.gone
                    font.strikeout: cell.gone
                    color: row.app.statusColor(cell.d.status)
                }
                OccupancyBar {
                    level: cell.d.occupancy
                    visible: row.app.showOccupancy && cell.d.occupancy > 0 && !cell.gone
                    Layout.alignment: Qt.AlignVCenter
                }
            }
            PlasmaComponents.Label {
                text: row.app.delayText(cell.d)
                font: Kirigami.Theme.smallFont
                color: row.app.statusColor(cell.d.status)
                elide: Text.ElideRight
                wrapMode: cell.gone ? Text.Wrap : Text.NoWrap
                Layout.fillWidth: true
            }
            PlasmaComponents.Label {
                visible: text !== "" && !cell.gone
                text: row.app.stopsText(cell.d)
                font: Kirigami.Theme.smallFont
                opacity: 0.7
                elide: Text.ElideRight
                Layout.fillWidth: true
            }
        }
    }
}
