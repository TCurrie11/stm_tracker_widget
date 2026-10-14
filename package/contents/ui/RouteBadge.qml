/*
    stm_tracker_widget: live STM bus and métro departures for KDE Plasma 6
    https://github.com/TCurrie11/stm_tracker_widget

    File:    package/contents/ui/RouteBadge.qml
    Purpose: Route number on the agency's route colour.
    Author:  TCurrie11 (https://github.com/TCurrie11)
    Version: 0.1.0
    Created: 2026-10-14
    Updated: 2026-10-14
    License: GPL-2.0-or-later

    SPDX-FileCopyrightText: 2026 TCurrie11
    SPDX-License-Identifier: GPL-2.0-or-later
*/

// A route number on the agency's route colour, like STM's signs.

import QtQuick

import org.kde.plasma.components as PlasmaComponents
import org.kde.kirigami as Kirigami

import "api.js" as Api

Rectangle {
    id: badge

    required property var route

    implicitWidth: Math.max(label.implicitWidth + Kirigami.Units.smallSpacing * 2, implicitHeight)
    implicitHeight: label.implicitHeight + Kirigami.Units.smallSpacing
    radius: route.mode === "metro" ? height / 2 : Kirigami.Units.cornerRadius
    color: route.color ? "#" + route.color : Kirigami.Theme.highlightColor

    Accessible.name: route.mode === "metro" ? i18n("Métro line %1", route.short_name) : i18n("Route %1", route.short_name)

    PlasmaComponents.Label {
        id: label
        anchors.centerIn: parent
        text: badge.route.short_name
        font.bold: true
        color: Api.textOn(badge.route.color)
    }
}
