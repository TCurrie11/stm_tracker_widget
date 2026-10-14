/*
    stm_tracker_widget: live STM bus and métro departures for KDE Plasma 6
    https://github.com/TCurrie11/stm_tracker_widget

    File:    package/contents/ui/CompactView.qml
    Purpose: Panel view: the next departure of the top route.
    Author:  TCurrie11 (https://github.com/TCurrie11)
    Version: 0.1.0
    Created: 2026-10-14
    Updated: 2026-10-14
    License: GPL-2.0-or-later

    SPDX-FileCopyrightText: 2026 TCurrie11
    SPDX-License-Identifier: GPL-2.0-or-later
*/

// Panel view: the next departure of the first configured route only,
// e.g. "[24] ▸ 4 min", coloured by status. Click opens the full view.

import QtQuick
import QtQuick.Layouts

import org.kde.plasma.plasmoid
import org.kde.plasma.core as PlasmaCore
import org.kde.plasma.components as PlasmaComponents
import org.kde.kirigami as Kirigami

MouseArea {
    id: compact

    required property var app

    readonly property bool vertical: Plasmoid.formFactor === PlasmaCore.Types.Vertical

    Layout.minimumWidth: vertical ? 0 : row.implicitWidth
    Layout.minimumHeight: vertical ? row.implicitHeight : 0

    hoverEnabled: true
    onClicked: app.expanded = !app.expanded

    RowLayout {
        id: row
        anchors.centerIn: parent
        spacing: Kirigami.Units.smallSpacing

        Kirigami.Icon {
            visible: !compact.app.topDeparture
            source: Plasmoid.icon
            Layout.preferredWidth: Kirigami.Units.iconSizes.smallMedium
            Layout.preferredHeight: Kirigami.Units.iconSizes.smallMedium
        }
        RouteBadge {
            visible: !!compact.app.topDeparture
            route: compact.app.topDeparture ? compact.app.topDeparture.group.route : { short_name: "", color: "" }
        }
        PlasmaComponents.Label {
            visible: !!compact.app.topDeparture
            text: compact.app.topDeparture ? "▸ " + compact.app.countdown(compact.app.topDeparture.departure) : ""
            color: compact.app.topDeparture ? compact.app.statusColor(compact.app.topDeparture.departure.status) : Kirigami.Theme.textColor
            opacity: (compact.app.topDeparture ? compact.app.statusOpacity(compact.app.topDeparture.departure.status) : 1) * (compact.app.reachable ? 1 : 0.6)
        }
    }
}
