/*
    SPDX-FileCopyrightText: 2026 Thomas Currie
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
