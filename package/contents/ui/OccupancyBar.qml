/*
    stm_tracker_widget: live STM bus and métro departures for KDE Plasma 6
    https://github.com/TCurrie11/stm_tracker_widget

    File:    package/contents/ui/OccupancyBar.qml
    Purpose: Three-segment bar showing how full a bus is.
    Author:  TCurrie11 (https://github.com/TCurrie11)
    Version: 0.1.0
    Created: 2026-10-14
    Updated: 2026-10-14
    License: GPL-2.0-or-later

    SPDX-FileCopyrightText: 2026 TCurrie11
    SPDX-License-Identifier: GPL-2.0-or-later
*/

pragma ComponentBehavior: Bound

// How full the bus is, as three segments: 1 seats free, 2 few seats,
// 3 standing room. The tooltip says it in words.

import QtQuick

import org.kde.plasma.components as PlasmaComponents
import org.kde.kirigami as Kirigami

Row {
    id: bar

    required property int level

    readonly property string label: [i18n("Occupancy unknown"), i18n("Seats free"), i18n("Few seats"), i18n("Standing room")][level] || ""

    spacing: Math.max(1, Kirigami.Units.smallSpacing / 2)
    Accessible.name: label

    Repeater {
        model: 3
        Rectangle {
            required property int index
            width: Math.round(Kirigami.Units.gridUnit * 0.3)
            height: Math.round(Kirigami.Units.gridUnit * 0.6)
            radius: 1
            color: Kirigami.Theme.textColor
            opacity: index < bar.level ? 0.85 : 0.2
        }
    }

    HoverHandler {
        id: hover
    }
    PlasmaComponents.ToolTip.visible: hover.hovered
    PlasmaComponents.ToolTip.text: label
}
