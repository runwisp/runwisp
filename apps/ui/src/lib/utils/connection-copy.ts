// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * User-facing copy for the "stalled" connection state — a live stream that was
 * opened but isn't responding. The honest message depends on whether this tab
 * is sharing one connection across tabs:
 *
 * - shared (the normal case): tab count is irrelevant — exactly one connection
 *   exists browser-wide — so a stall means that single connection isn't
 *   responding (e.g. a reverse proxy buffering the SSE response). Don't blame tabs.
 * - degraded (no Web Locks / BroadcastChannel): every tab holds its own
 *   EventSource, so too many open tabs really can exhaust the browser's
 *   per-origin connection limit. Here, telling the operator to close tabs is
 *   the correct fix.
 */
export interface StalledCopy {
    /** Short label for the status chip. */
    label: string;
    /** One-line hint shown under the label. */
    hint: string;
    /** Tooltip / aria description. */
    title: string;
    /** Panel heading. */
    heading: string;
    /** Panel body paragraph. */
    body: string;
}

export function stalledCopy(shared: boolean): StalledCopy {
    if (shared) {
        return {
            label: "Updates paused",
            hint: "Reconnecting…",
            title: "Live updates aren't responding right now. The connection will resume automatically when it recovers — no refresh needed.",
            heading: "Live updates stalled",
            body: "The live-updates connection isn't responding right now. This usually clears on its own; the view will catch up automatically when it does.",
        };
    }
    return {
        label: "Updates paused",
        hint: "Close extra tabs",
        title: "Live updates are paused: too many RunWisp tabs are open in this browser, which exhausts the browser's connection limit. Close some tabs to resume — it recovers on its own.",
        heading: "Live updates paused",
        body: "Too many RunWisp tabs are open in this browser, so it hit its limit on simultaneous connections. Close some other RunWisp tabs and live updates will resume here automatically — no refresh needed.",
    };
}
