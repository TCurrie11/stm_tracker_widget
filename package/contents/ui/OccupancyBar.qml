pragma ComponentBehavior: Bound

/*
    SPDX-FileCopyrightText: 2026 Thomas Currie
    SPDX-License-Identifier: GPL-2.0-or-later
*/

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
