// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Shared browser-side harness for replaying a captured TUI PTY stream into a
// real xterm.js terminal: the terminal-window chrome, the embedded font, and
// the xterm asset paths. Used by both tui.screenshots.ts (one still frame per
// docs screen) and tui.demo.ts (one continuous animated tour). Keeping COLS/
// ROWS/chrome here means the two can never drift apart.

import { type Page } from "@playwright/test";
import { createRequire } from "node:module";
import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import type { ITerminalOptions } from "@xterm/xterm";

const require = createRequire(import.meta.url);

// The pty/capture size (must match screenCols/screenRows in the Go harness,
// apps/runwisp/tests/e2e/harness_test.go). 138 cols makes the TUI main pane
// exactly MaxContentWidth, so the exec table fills it with no orphaned
// right-hand gap (see the Go harness comment).
export const COLS = 138;
export const ROWS = 40;

export const xtermJs = require.resolve("@xterm/xterm");
export const xtermCss = join(dirname(xtermJs), "../css/xterm.css");
const fontDir = join(
    dirname(require.resolve("@fontsource-variable/geist-mono/package.json")),
    "files",
);

// One variable face covers the whole 100–900 range, so regular and bold cells
// both come from a single embedded woff2.
export async function fontFaceCss(): Promise<string> {
    const data = await readFile(join(fontDir, "geist-mono-latin-wght-normal.woff2"));
    return `@font-face{font-family:'Geist Mono Variable';font-style:normal;font-weight:100 900;font-display:block;src:url(data:font/woff2;base64,${data.toString("base64")}) format('woff2-variations');}`;
}

// A terminal-window frame: rounded body, a title bar with traffic lights and a
// centred "RunWisp", and the xterm mount below. The frame background tracks the
// TUI's own dark base so any unpainted cell blends in; everything outside the
// rounded corners is transparent in the captured PNG (see omitBackground).
export const HARNESS_HTML = `
<div id="frame">
  <div id="titlebar">
    <span class="dots"><i></i><i></i><i></i></span>
    <span id="title">RunWisp</span>
  </div>
  <div id="term"></div>
</div>`;

export const CHROME_CSS = `
*{box-sizing:border-box;}
html,body{margin:0;background:transparent;}
#frame{display:inline-block;background:#0d1117;border-radius:10px;overflow:hidden;}
#titlebar{position:relative;height:34px;display:flex;align-items:center;
  background:#1c2230;border-bottom:1px solid #000;}
#titlebar .dots{display:flex;gap:8px;padding-left:14px;}
#titlebar .dots i{width:12px;height:12px;border-radius:50%;background:#3a4254;display:block;}
#title{position:absolute;left:0;right:0;text-align:center;color:#9aa4b2;
  font:600 13px/1 -apple-system,system-ui,sans-serif;pointer-events:none;}
#term{padding:4px;}
.xterm{cursor:default;}`;

// Loads the frame markup, fonts, and xterm.js into page. Callers construct the
// Terminal themselves (see terminalOptions below) so they can await write()
// per chunk instead of all at once.
export async function setupTerminalPage(page: Page): Promise<void> {
    await page.setContent(HARNESS_HTML);
    await page.addStyleTag({ path: xtermCss });
    await page.addStyleTag({ content: await fontFaceCss() });
    await page.addStyleTag({ content: CHROME_CSS });
    await page.addScriptTag({ path: xtermJs });
}

// The shared Terminal construction options: DOM renderer (the canvas addon
// mis-scales under deviceScaleFactor>1, clipping to a quadrant), Geist Mono,
// and the TUI's own dark theme. Truecolor (38;2) in the stream is exact RGB;
// the theme only colours unpainted cells and the default fg/bg. Plain data, so
// callers can pass it straight into page.evaluate.
export const TERMINAL_OPTIONS: ITerminalOptions = {
    cols: COLS,
    rows: ROWS,
    fontFamily: "'Geist Mono Variable', monospace",
    fontSize: 15,
    lineHeight: 1.0,
    letterSpacing: 0,
    theme: { background: "#0d1117", foreground: "#c9d1d9" },
};

// Both replay specs run window.Terminal (loaded via the xterm.js <script> tag
// above) inside page.evaluate; declared once here so it applies program-wide.
declare global {
    interface Window {
        Terminal: typeof import("@xterm/xterm").Terminal;
    }
}
