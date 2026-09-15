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
import { mkdir, readFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { TERMINAL_OPTIONS, setupTerminalPage } from "./tui-harness";

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

interface RenderArgs {
    base64: string;
    options: typeof TERMINAL_OPTIONS;
}

// Runs in the browser: load the font, build the terminal, replay the captured
// stream, and resolve once xterm reports the write is flushed.
async function renderFrame({ base64, options }: RenderArgs): Promise<void> {
    await document.fonts.load("16px 'Geist Mono Variable'");
    await document.fonts.load("bold 16px 'Geist Mono Variable'");
    await document.fonts.ready;

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

            await setupTerminalPage(page);

            await page.evaluate(renderFrame, {
                base64: ansi.toString("base64"),
                options: TERMINAL_OPTIONS,
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
