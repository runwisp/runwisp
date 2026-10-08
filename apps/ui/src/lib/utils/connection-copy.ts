// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Copy for the "stalled" connection state. With a shared cross-tab connection
// a stall is a single unresponsive stream (often a buffering proxy), so tabs
// are not to blame; without one, each tab holds its own EventSource and too
// many tabs really can exhaust the browser's per-origin connection limit.
interface StalledCopy {
    label: string;
    hint: string;
    title: string;
    heading: string;
    body: string;
}

export function stalledCopy(shared: boolean): StalledCopy {
    if (shared) {
        return {
            label: "Updates paused",
            hint: "Reconnecting…",
            title: "Live updates aren't responding right now. The connection will resume automatically when it recovers, no refresh needed.",
            heading: "Live updates stalled",
            body: "The live-updates connection isn't responding right now. This usually clears on its own; the view will catch up automatically when it does.",
        };
    }
    return {
        label: "Updates paused",
        hint: "Close extra tabs",
        title: "Live updates are paused: too many RunWisp tabs are open in this browser, which exhausts the browser's connection limit. Close some tabs to resume; it recovers on its own.",
        heading: "Live updates paused",
        body: "Too many RunWisp tabs are open in this browser, so it hit its limit on simultaneous connections. Close some other RunWisp tabs and live updates will resume here automatically, no refresh needed.",
    };
}
