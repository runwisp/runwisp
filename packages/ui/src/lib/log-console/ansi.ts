// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import Convert from "ansi-to-html";

// Theme-derived 16-color ANSI palette. ansi-to-html inlines these values
// verbatim into style attributes, so CSS var() references resolve against
// the --rw-ansi-* tokens in theme-tokens.css. Codes 16-255 keep the
// library's computed RGB defaults.
const ANSI_PALETTE: Record<number, string> = Object.freeze({
    0: "var(--rw-ansi-black)",
    1: "var(--rw-ansi-red)",
    2: "var(--rw-ansi-green)",
    3: "var(--rw-ansi-yellow)",
    4: "var(--rw-ansi-blue)",
    5: "var(--rw-ansi-magenta)",
    6: "var(--rw-ansi-cyan)",
    7: "var(--rw-ansi-white)",
    8: "var(--rw-ansi-bright-black)",
    9: "var(--rw-ansi-bright-red)",
    10: "var(--rw-ansi-bright-green)",
    11: "var(--rw-ansi-bright-yellow)",
    12: "var(--rw-ansi-bright-blue)",
    13: "var(--rw-ansi-bright-magenta)",
    14: "var(--rw-ansi-bright-cyan)",
    15: "var(--rw-ansi-bright-white)",
});

// Convert ANSI escape sequences in a single log line to safe HTML spans.
// A fresh converter per call ensures no colour state leaks between
// independently virtualised lines.
export function ansiLineToHtml(text: string): string {
    return new Convert({
        escapeXML: true,
        fg: "var(--rw-color-mist-200)",
        bg: "var(--rw-color-mist-950)",
        colors: ANSI_PALETTE,
    }).toHtml(text);
}

// Matches CSI escape sequences (colour codes, cursor moves, etc.), which
// render to zero visible width. The ESC control char is intentional here.
// eslint-disable-next-line no-control-regex
const ANSI_ESCAPE = /\x1b\[[0-9;?]*[A-Za-z]/g;
const TAB_WIDTH = 8;

// Count the visible columns a log line occupies once ANSI escape sequences
// are stripped. Tabs are charged the full tab width — an over-estimate that
// only adds trailing slack to the horizontal scroll surface, never clips.
export function visibleColumns(text: string): number {
    let columns = 0;
    for (const ch of text.replace(ANSI_ESCAPE, "")) {
        columns += ch === "\t" ? TAB_WIDTH : 1;
    }
    return columns;
}
