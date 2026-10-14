/*
    stm_tracker_widget: live STM bus and métro departures for KDE Plasma 6
    https://github.com/TCurrie11/stm_tracker_widget

    File:    package/contents/ui/api.js
    Purpose: Requests to the local transitd service.
    Author:  TCurrie11 (https://github.com/TCurrie11)
    Version: 0.1.0
    Created: 2026-10-14
    Updated: 2026-10-14
    License: GPL-2.0-or-later

    SPDX-FileCopyrightText: 2026 TCurrie11
    SPDX-License-Identifier: GPL-2.0-or-later
*/

// Requests to the local transitd service. Every call takes a callback
// callback(error, data): error is null on success, otherwise a short string for
// console.warn; the views never show it to the user.
.pragma library

function get(url, callback) {
    var xhr = new XMLHttpRequest();
    xhr.onreadystatechange = function () {
        if (xhr.readyState !== XMLHttpRequest.DONE) {
            return;
        }
        if (xhr.status === 0) {
            callback("transitd unreachable at " + url, null);
            return;
        }
        var data = null;
        try {
            data = JSON.parse(xhr.responseText);
        } catch (e) {
            callback("bad JSON from " + url + ": " + e, null);
            return;
        }
        if (xhr.status !== 200) {
            callback("HTTP " + xhr.status + " from " + url + ": " + (data && data.error), null);
            return;
        }
        callback(null, data);
    };
    xhr.open("GET", url);
    xhr.send();
}

function base(serviceUrl) {
    return serviceUrl.replace(/\/+$/, "");
}

function departures(serviceUrl, stop, routes, perDirection, callback) {
    var url = base(serviceUrl) + "/v1/departures?stop=" + encodeURIComponent(stop) + "&n=" + perDirection;
    if (routes && routes.length > 0) {
        url += "&routes=" + routes.map(encodeURIComponent).join(",");
    }
    get(url, callback);
}

function nearby(serviceUrl, address, callback) {
    get(base(serviceUrl) + "/v1/stops/nearby?radius=500&address=" + encodeURIComponent(address), callback);
}

function stop(serviceUrl, id, callback) {
    get(base(serviceUrl) + "/v1/stops/" + encodeURIComponent(id), callback);
}

function stations(serviceUrl, callback) {
    get(base(serviceUrl) + "/v1/stations", callback);
}

// parseWatches reads the watches setting, tolerating a hand-edited or
// empty value.
function parseWatches(json) {
    try {
        var parsed = JSON.parse(json || "[]");
        return Array.isArray(parsed) ? parsed.filter(function (watch) { return watch && watch.stop; }) : [];
    } catch (e) {
        console.warn("stmtransit: ignoring unreadable watches setting:", e);
        return [];
    }
}

// minutesUntil rounds down, like the countdown on STM's own signs.
function minutesUntil(iso, now) {
    return Math.floor((new Date(iso).getTime() - now.getTime()) / 60000);
}

// textOn picks black or white text for a route colour badge.
function textOn(hex) {
    if (!hex || hex.length !== 6) {
        return "white";
    }
    var red = parseInt(hex.substr(0, 2), 16), green = parseInt(hex.substr(2, 2), 16), blue = parseInt(hex.substr(4, 2), 16);
    // Perceived brightness (ITU-R BT.601 weights).
    return (0.299 * red + 0.587 * green + 0.114 * blue) > 160 ? "black" : "white";
}
