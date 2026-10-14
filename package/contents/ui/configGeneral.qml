/*
    stm_tracker_widget: live STM bus and métro departures for KDE Plasma 6
    https://github.com/TCurrie11/stm_tracker_widget

    File:    package/contents/ui/configGeneral.qml
    Purpose: Settings page: find stops, pick routes, display options.
    Author:  TCurrie11 (https://github.com/TCurrie11)
    Version: 0.1.0
    Created: 2026-10-14
    Updated: 2026-10-14
    License: GPL-2.0-or-later

    SPDX-FileCopyrightText: 2026 TCurrie11
    SPDX-License-Identifier: GPL-2.0-or-later
*/

pragma ComponentBehavior: Bound

// Settings: find stops near an address, pick routes, order the list (the
// first stop's first route is what the panel shows), plus display options.
// Everything is stored by Plasma; transitd keeps no user settings.

import QtQuick
import QtQuick.Controls as QQC2
import QtQuick.Layouts

import org.kde.kirigami as Kirigami
import org.kde.kcmutils as KCM

import "api.js" as Api

KCM.SimpleKCM {
    id: page

    property string cfg_serviceUrl
    property string cfg_watches
    property int cfg_refreshSeconds
    property int cfg_departuresPerDirection
    property string cfg_earlyColor
    property bool cfg_showOccupancy
    property string cfg_address

    readonly property var watchList: Api.parseWatches(cfg_watches)
    property var results: []      // nearby search results
    property var stations: []     // métro stations for the home station list
    property var routesByStop: ({}) // stop ID -> routes, for the route checkboxes
    property bool searching: false
    property string problem: ""   // plain message; details go to console.warn

    // ---- editing the list ----

    function save(list) {
        cfg_watches = JSON.stringify(list);
    }

    function indexOf(stopId) {
        return watchList.findIndex(watch => watch.stop === stopId);
    }

    function isMetro(stop) {
        return stop.routes.length > 0 && stop.routes.every(route => route.mode === "metro");
    }

    function toggleStop(stop, on) {
        let list = watchList.slice();
        const existing = indexOf(stop.id);
        if (on && existing < 0) {
            list.push({ stop: stop.id, stopName: stop.name, routes: [], metro: isMetro(stop) });
            remember(stop);
        } else if (!on && existing >= 0) {
            list.splice(existing, 1);
        }
        save(list);
    }

    function toggleRoute(index, route, on) {
        let list = watchList.slice();
        let watch = Object.assign({}, list[index]);
        watch.routes = (watch.routes || []).filter(name => name !== route);
        if (on) {
            watch.routes.push(route);
        }
        list[index] = watch;
        save(list);
    }

    function move(index, delta) {
        let list = watchList.slice();
        const target = index + delta;
        if (target < 0 || target >= list.length) {
            return;
        }
        [list[index], list[target]] = [list[target], list[index]];
        save(list);
    }

    function removeAt(index) {
        let list = watchList.slice();
        list.splice(index, 1);
        save(list);
    }

    // The home station is the first métro watch. Choosing another replaces
    // it in place; "None" removes it.
    function setHomeStation(station) {
        let list = watchList.slice();
        const current = list.findIndex(watch => watch.metro);
        if (!station) {
            if (current >= 0) {
                list.splice(current, 1);
            }
        } else {
            const home = { stop: station.id, stopName: station.name, routes: [], metro: true };
            if (current >= 0) {
                list[current] = home;
            } else {
                list.push(home);
            }
            remember(station);
        }
        save(list);
    }

    function remember(stop) {
        let known = Object.assign({}, routesByStop);
        known[stop.id] = stop.routes;
        routesByStop = known;
    }

    // ---- talking to transitd ----

    function failed(error) {
        console.warn("stmtransit settings:", error);
        problem = i18n("Can't reach the transit service. Is transitd running?");
    }

    function findStops() {
        const address = addressField.text.trim();
        if (address === "") {
            return;
        }
        cfg_address = address;
        searching = true;
        Api.nearby(cfg_serviceUrl, address, (error, data) => {
            searching = false;
            if (error) {
                if (error.indexOf("HTTP 404") >= 0) {
                    problem = i18n("No address found. Try adding the city, e.g. \"1500 rue Berri, Montréal\".");
                } else {
                    failed(error);
                }
                results = [];
                return;
            }
            problem = "";
            results = data.stops;
            if (data.stops.length === 0) {
                problem = i18n("No stops within 500 m of %1.", data.place.label);
            }
        });
    }

    function loadRoutes() {
        for (const watch of watchList) {
            if (routesByStop[watch.stop] !== undefined) {
                continue;
            }
            Api.stop(cfg_serviceUrl, watch.stop, (error, stop) => {
                if (error) {
                    failed(error);
                    return;
                }
                remember(stop);
            });
        }
    }

    function loadStations() {
        Api.stations(cfg_serviceUrl, (error, data) => {
            if (error) {
                failed(error);
                return;
            }
            stations = data.stations.filter(station => isMetro(station));
            syncHomeStation();
        });
    }

    function syncHomeStation() {
        const home = watchList.find(watch => watch.metro);
        homeStation.currentIndex = home ? stations.findIndex(station => station.id === home.stop) + 1 : 0;
    }

    onWatchListChanged: {
        loadRoutes();
        syncHomeStation();
    }
    Component.onCompleted: {
        loadRoutes();
        loadStations();
    }

    // ---- layout ----

    Kirigami.FormLayout {
        Kirigami.InlineMessage {
            Layout.fillWidth: true
            visible: page.problem !== ""
            type: Kirigami.MessageType.Warning
            text: page.problem
        }

        Kirigami.Separator {
            Kirigami.FormData.isSection: true
            Kirigami.FormData.label: i18n("Find stops")
        }

        RowLayout {
            Kirigami.FormData.label: i18n("Address:")
            Layout.fillWidth: true
            QQC2.TextField {
                id: addressField
                Layout.fillWidth: true
                Layout.minimumWidth: Kirigami.Units.gridUnit * 14
                text: page.cfg_address
                placeholderText: i18n("e.g. 1500 rue Berri")
                onAccepted: page.findStops()
            }
            QQC2.Button {
                text: i18n("Find stops")
                icon.name: "search"
                enabled: !page.searching && addressField.text.trim() !== ""
                onClicked: page.findStops()
            }
            QQC2.BusyIndicator {
                visible: page.searching
                running: visible
                Layout.preferredHeight: Kirigami.Units.iconSizes.medium
            }
        }

        ColumnLayout {
            visible: page.results.length > 0
            Layout.fillWidth: true
            spacing: 0
            Repeater {
                model: page.results
                delegate: RowLayout {
                    id: result
                    required property var modelData
                    spacing: Kirigami.Units.smallSpacing
                    QQC2.CheckBox {
                        text: i18nc("stop name, distance", "%1 — %2 m", result.modelData.name, result.modelData.meters)
                        checked: page.indexOf(result.modelData.id) >= 0
                        onToggled: page.toggleStop(result.modelData, checked)
                    }
                    Repeater {
                        model: result.modelData.routes
                        delegate: RouteBadge {
                            required property var modelData
                            route: modelData
                        }
                    }
                }
            }
        }

        Kirigami.Separator {
            Kirigami.FormData.isSection: true
            Kirigami.FormData.label: i18n("Shown in the widget")
        }

        QQC2.Label {
            visible: page.watchList.length === 0
            text: i18n("No stops yet. Find one above.")
            opacity: 0.7
        }

        ColumnLayout {
            visible: page.watchList.length > 0
            Layout.fillWidth: true
            spacing: Kirigami.Units.largeSpacing

            QQC2.Label {
                Layout.fillWidth: true
                wrapMode: Text.Wrap
                opacity: 0.7
                font: Kirigami.Theme.smallFont
                text: i18n("The panel shows the first stop's first ticked route. With no routes ticked, every route at the stop is shown.")
            }

            Repeater {
                model: page.watchList
                delegate: ColumnLayout {
                    id: entry
                    required property var modelData
                    required property int index
                    readonly property var routes: page.routesByStop[modelData.stop] || []
                    spacing: 0
                    Layout.fillWidth: true

                    RowLayout {
                        Layout.fillWidth: true
                        Kirigami.Icon {
                            source: entry.index === 0 ? "favorite" : "mark-location"
                            Layout.preferredWidth: Kirigami.Units.iconSizes.small
                            Layout.preferredHeight: Kirigami.Units.iconSizes.small
                        }
                        QQC2.Label {
                            text: entry.modelData.stopName || entry.modelData.stop
                            font.bold: true
                            elide: Text.ElideRight
                            Layout.fillWidth: true
                        }
                        QQC2.ToolButton {
                            icon.name: "go-up"
                            enabled: entry.index > 0
                            onClicked: page.move(entry.index, -1)
                            QQC2.ToolTip.text: i18n("Move up")
                            QQC2.ToolTip.visible: hovered
                            Accessible.name: QQC2.ToolTip.text
                        }
                        QQC2.ToolButton {
                            icon.name: "go-down"
                            enabled: entry.index < page.watchList.length - 1
                            onClicked: page.move(entry.index, 1)
                            QQC2.ToolTip.text: i18n("Move down")
                            QQC2.ToolTip.visible: hovered
                            Accessible.name: QQC2.ToolTip.text
                        }
                        QQC2.ToolButton {
                            icon.name: "list-remove"
                            onClicked: page.removeAt(entry.index)
                            QQC2.ToolTip.text: i18n("Remove")
                            QQC2.ToolTip.visible: hovered
                            Accessible.name: QQC2.ToolTip.text
                        }
                    }
                    Flow {
                        visible: !entry.modelData.metro && entry.routes.length > 1
                        Layout.fillWidth: true
                        Layout.leftMargin: Kirigami.Units.iconSizes.small + Kirigami.Units.smallSpacing
                        spacing: Kirigami.Units.smallSpacing
                        Repeater {
                            model: entry.routes
                            delegate: QQC2.CheckBox {
                                required property var modelData
                                text: modelData.short_name
                                checked: (entry.modelData.routes || []).includes(modelData.short_name)
                                onToggled: page.toggleRoute(entry.index, modelData.short_name, checked)
                            }
                        }
                    }
                }
            }
        }

        Kirigami.Separator {
            Kirigami.FormData.isSection: true
            Kirigami.FormData.label: i18n("Métro")
        }

        QQC2.ComboBox {
            id: homeStation
            Kirigami.FormData.label: i18n("Home station:")
            Layout.minimumWidth: Kirigami.Units.gridUnit * 14
            model: [i18n("None")].concat(page.stations.map(station => station.name))
            onActivated: index => page.setHomeStation(index === 0 ? null : page.stations[index - 1])
        }

        Kirigami.Separator {
            Kirigami.FormData.isSection: true
            Kirigami.FormData.label: i18n("Display")
        }

        QQC2.SpinBox {
            Kirigami.FormData.label: i18n("Update every:")
            from: 30
            to: 600
            stepSize: 30
            value: page.cfg_refreshSeconds
            onValueModified: page.cfg_refreshSeconds = value
            textFromValue: (v, locale) => i18np("%1 second", "%1 seconds", v)
            valueFromText: (text, locale) => parseInt(text)
        }

        QQC2.SpinBox {
            Kirigami.FormData.label: i18n("Departures per direction:")
            from: 1
            to: 5
            value: page.cfg_departuresPerDirection
            onValueModified: page.cfg_departuresPerDirection = value
        }

        QQC2.ComboBox {
            Kirigami.FormData.label: i18n("Early buses:")
            model: [i18n("Orange"), i18n("Green")]
            currentIndex: page.cfg_earlyColor === "green" ? 1 : 0
            onActivated: index => page.cfg_earlyColor = index === 1 ? "green" : "orange"
        }

        QQC2.CheckBox {
            text: i18n("Show how full buses are")
            checked: page.cfg_showOccupancy
            onToggled: page.cfg_showOccupancy = checked
        }

        Kirigami.Separator {
            Kirigami.FormData.isSection: true
            Kirigami.FormData.label: i18n("Advanced")
        }

        QQC2.TextField {
            Kirigami.FormData.label: i18n("Service address:")
            Layout.minimumWidth: Kirigami.Units.gridUnit * 14
            text: page.cfg_serviceUrl
            onEditingFinished: page.cfg_serviceUrl = text.trim()
        }
    }
}
