// Requests to the local transitd service. Every call takes a callback
// cb(error, data): error is null on success, otherwise a short string for
// console.warn; the views never show it to the user.
.pragma library

function get(url, cb) {
    var xhr = new XMLHttpRequest();
    xhr.onreadystatechange = function () {
        if (xhr.readyState !== XMLHttpRequest.DONE) {
            return;
        }
        if (xhr.status === 0) {
            cb("transitd unreachable at " + url, null);
            return;
        }
        var data = null;
        try {
            data = JSON.parse(xhr.responseText);
        } catch (e) {
            cb("bad JSON from " + url + ": " + e, null);
            return;
        }
        if (xhr.status !== 200) {
            cb("HTTP " + xhr.status + " from " + url + ": " + (data && data.error), null);
            return;
        }
        cb(null, data);
    };
    xhr.open("GET", url);
    xhr.send();
}

function base(serviceUrl) {
    return serviceUrl.replace(/\/+$/, "");
}

function departures(serviceUrl, stop, routes, n, cb) {
    var url = base(serviceUrl) + "/v1/departures?stop=" + encodeURIComponent(stop) + "&n=" + n;
    if (routes && routes.length > 0) {
        url += "&routes=" + routes.map(encodeURIComponent).join(",");
    }
    get(url, cb);
}

function nearby(serviceUrl, address, cb) {
    get(base(serviceUrl) + "/v1/stops/nearby?radius=500&address=" + encodeURIComponent(address), cb);
}

function stop(serviceUrl, id, cb) {
    get(base(serviceUrl) + "/v1/stops/" + encodeURIComponent(id), cb);
}

function stations(serviceUrl, cb) {
    get(base(serviceUrl) + "/v1/stations", cb);
}

// parseWatches reads the watches setting, tolerating a hand-edited or
// empty value.
function parseWatches(json) {
    try {
        var w = JSON.parse(json || "[]");
        return Array.isArray(w) ? w.filter(function (x) { return x && x.stop; }) : [];
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
    var r = parseInt(hex.substr(0, 2), 16), g = parseInt(hex.substr(2, 2), 16), b = parseInt(hex.substr(4, 2), 16);
    return (0.299 * r + 0.587 * g + 0.114 * b) > 160 ? "black" : "white";
}
