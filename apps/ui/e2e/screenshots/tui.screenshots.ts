// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Generates the TUI screenshots embedded in the docs (apps/docs). The capture
// half (apps/runwisp tests/e2e TestCaptureTUIScreenshots) drives the real
// `runwisp tui` against a demo-seeded daemon and dumps the raw terminal stream
// per screen to RUNWISP_TUI_FRAMES/tui-<name>.ansi. This spec replays each
// stream into xterm.js — a real terminal emulator — inside chromium and
// screenshots a window-chromed container, so the PNGs match what an operator
// actually sees. Run via `bun run screenshots`.
//
// Like the Web UI shots, these are illustrative, on-demand, committed assets:
// the exact seeded runs and relative timestamps drift between regenerations.

import { test, expect } from "@playwright/test";
import { createRequire } from "node:module";
import { mkdir, readFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import type { Terminal, ITerminalOptions } from "@xterm/xterm";

const require = createRequire(import.meta.url);
const __dirname = dirname(fileURLToPath(import.meta.url));
const OUT_DIR = resolve(__dirname, "../../../docs/src/assets/screenshots");

const FRAMES_DIR = process.env.RUNWISP_TUI_FRAMES;

// Each docs PNG ↔ a captured frame of the same name (tui-<name>.ansi → tui-<name>.png).
const SHOTS = [
    "home",
    "task-detail",
    "info",
    "run-detail",
    "fullscreen-mode",
    "quit-confirmation",
] as const;

// The pty/capture size (must match screenCols/screenRows in the Go harness).
// 138 cols makes the TUI main pane exactly MaxContentWidth, so the exec table
// fills it with no orphaned right-hand gap (see the Go harness comment).
const COLS = 138;
const ROWS = 40;

const xtermJs = require.resolve("@xterm/xterm");
const xtermCss = join(dirname(xtermJs), "../css/xterm.css");
const fontDir = join(dirname(require.resolve("@fontsource/jetbrains-mono/package.json")), "files");

declare global {
    interface Window {
        Terminal: typeof Terminal;
    }
}

async function fontFaceCss(): Promise<string> {
    const face = async (weight: number): Promise<string> => {
        const data = await readFile(join(fontDir, `jetbrains-mono-latin-${weight}-normal.woff2`));
        return `@font-face{font-family:'JetBrains Mono';font-style:normal;font-weight:${weight};font-display:block;src:url(data:font/woff2;base64,${data.toString("base64")}) format('woff2');}`;
    };
    return (await Promise.all([face(400), face(700)])).join("\n");
}

// A terminal-window frame: rounded body, a title bar with traffic lights and a
// centred "RunWisp", and the xterm mount below. The frame background tracks the
// TUI's own dark base so any unpainted cell blends in; everything outside the
// rounded corners is transparent in the captured PNG (see omitBackground).
const HARNESS_HTML = `
<div id="frame">
  <div id="titlebar">
    <span class="dots"><i></i><i></i><i></i></span>
    <span id="title">RunWisp</span>
  </div>
  <div id="term"></div>
</div>`;

const CHROME_CSS = `
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

interface RenderArgs {
    base64: string;
    cols: number;
    rows: number;
}

// Runs in the browser: load the font, build the terminal, replay the captured
// stream, and resolve once xterm reports the write is flushed.
async function renderFrame({ base64, cols, rows }: RenderArgs): Promise<void> {
    await document.fonts.load("16px 'JetBrains Mono'");
    await document.fonts.load("bold 16px 'JetBrains Mono'");
    await document.fonts.ready;

    const options: ITerminalOptions = {
        cols,
        rows,
        fontFamily: "'JetBrains Mono', monospace",
        fontSize: 15,
        lineHeight: 1.0,
        letterSpacing: 0,
        // Truecolor (38;2) in the stream is exact RGB; the theme only colours
        // unpainted cells and the default fg/bg.
        theme: { background: "#0d1117", foreground: "#c9d1d9" },
    };
    // Default (DOM) renderer: it honours deviceScaleFactor for crisp retina
    // output and draws box-drawing/braille glyphs from the loaded font. The
    // canvas addon mis-scales under deviceScaleFactor>1 (clips to a quadrant),
    // so it is deliberately not used.
    const term = new window.Terminal(options);
    const mount = document.getElementById("term");
    if (!mount) throw new Error("missing #term mount");
    term.open(mount);

    const binary = atob(base64);
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);

    await new Promise<void>((res) => term.write(bytes, res));
}

// Skip the whole group unless the Go capture has produced frames. This keeps
// the web-only screenshots config runnable on its own (and mirrors the capture
// test's RUNWISP_TUI_SHOOT_DIR gate). The moon `screenshots` task sets it.
test.describe("tui", () => {
    test.skip(
        !FRAMES_DIR,
        "set RUNWISP_TUI_FRAMES (via the screenshots moon task) to regenerate TUI screenshots",
    );

    test.beforeAll(async () => {
        await mkdir(OUT_DIR, { recursive: true });
    });

    for (const name of SHOTS) {
        test(name, async ({ page }) => {
            if (!FRAMES_DIR) throw new Error("RUNWISP_TUI_FRAMES unset");
            const ansi = await readFile(join(FRAMES_DIR, `tui-${name}.ansi`));

            await page.setContent(HARNESS_HTML);
            await page.addStyleTag({ path: xtermCss });
            await page.addStyleTag({ content: await fontFaceCss() });
            await page.addStyleTag({ content: CHROME_CSS });
            await page.addScriptTag({ path: xtermJs });

            await page.evaluate(renderFrame, {
                base64: ansi.toString("base64"),
                cols: COLS,
                rows: ROWS,
            });

            // Let the renderer paint the final frame before grabbing it.
            await page.waitForTimeout(400);

            const frame = page.locator("#frame");
            await expect(frame).toBeVisible();
            // omitBackground keeps the area outside the rounded corners
            // transparent in the PNG (the body background is transparent too).
            await frame.screenshot({
                path: join(OUT_DIR, `tui-${name}.png`),
                omitBackground: true,
            });
        });
    }
});
