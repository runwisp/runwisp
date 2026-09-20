// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// The RunWisp TUI showcase clip (README hero). Replays a single continuous PTY
// stream captured by the Go harness (apps/runwisp tests/e2e
// TestCaptureTUIDemo) into a real xterm.js terminal, framed in the same
// terminal-window chrome as the docs screenshots (tui-harness.ts). The
// captured stream is split at markTime: everything before it (daemon connect,
// initial paint) replays as an instant fast-forward, so the clip opens on the
// clean, populated Home screen with no "Connecting…" flash; everything at or
// after markTime is paced in real time and captured via a DevTools screencast
// (screencast.ts). scripts/encode-demo-video.sh turns the frames into the
// committed animated WebP. Run via `bun run demo-tui`; not part of
// `bun run ci`.

import { expect, test } from "@playwright/test";
import { readFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { TERMINAL_OPTIONS, setupTerminalPage } from "./tui-harness";
import { Screencast } from "./screencast";

const __dirname = dirname(fileURLToPath(import.meta.url));
const CAST_FILE = process.env.RUNWISP_TUI_CAST_FILE;
// Lossless PNG frames land here (under the gitignored test-results/ dir);
// scripts/encode-demo-video.sh reads frames.txt from it, must match the
// DEMO_REC_SUBDIR the moon `demo-tui` task passes it.
const FRAMES_DIR = resolve(__dirname, "../../test-results/tui-demo/frames");

interface CastChunk {
    t: number;
    d: string;
}

interface CastFile {
    markTime: number;
    entries: CastChunk[];
}

// Validates the JSON written by the Go harness (writeCast in
// apps/runwisp/tests/e2e/tui_demo_test.go) without an `as` cast.
function parseCastFile(raw: string): CastFile {
    const data: unknown = JSON.parse(raw);
    if (typeof data !== "object" || data === null) {
        throw new Error("invalid TUI cast file: not an object");
    }
    if (!("markTime" in data) || typeof data.markTime !== "number") {
        throw new Error("invalid TUI cast file: missing markTime");
    }
    if (!("entries" in data) || !Array.isArray(data.entries)) {
        throw new Error("invalid TUI cast file: missing entries");
    }

    const entries = data.entries.map((entry, index) => {
        if (
            typeof entry !== "object" ||
            entry === null ||
            !("t" in entry) ||
            !("d" in entry) ||
            typeof entry.t !== "number" ||
            typeof entry.d !== "string"
        ) {
            throw new Error(`invalid TUI cast file: bad entry at index ${index}`);
        }
        return { t: entry.t, d: entry.d };
    });

    return { markTime: data.markTime, entries };
}

declare global {
    interface Window {
        __tuiTerm?: import("@xterm/xterm").Terminal;
    }
}

// Runs in the browser, before the screencast starts: builds and opens the
// terminal so its grid has its real dark, correctly-sized layout in the very
// first captured frame, instead of the screencast's first frame catching the
// bare (zero-height) #term mount before xterm has painted into it.
async function createTerminal(options: typeof TERMINAL_OPTIONS): Promise<void> {
    await document.fonts.load("16px 'Geist Mono Variable'");
    await document.fonts.load("bold 16px 'Geist Mono Variable'");
    await document.fonts.ready;

    const term = new window.Terminal(options);
    const mount = document.getElementById("term");
    if (!mount) throw new Error("missing #term mount");
    term.open(mount);
    window.__tuiTerm = term;
}

// Runs in the browser: fast-forwards every chunk before markTime with no delay
// (reconstructing the correct starting screen without spending clip time on
// it), then replays the rest with its original spacing.
async function replayCast(cast: CastFile): Promise<void> {
    const term = window.__tuiTerm;
    if (!term) throw new Error("terminal not initialized");

    let lastPaced = cast.markTime;
    for (const chunk of cast.entries) {
        if (chunk.t >= cast.markTime) {
            const gapMs = Math.max(0, (chunk.t - lastPaced) * 1000);
            if (gapMs > 0) await new Promise((res) => setTimeout(res, gapMs));
            lastPaced = chunk.t;
        }

        const binary = atob(chunk.d);
        const bytes = new Uint8Array(binary.length);
        for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
        await new Promise<void>((res) => term.write(bytes, res));
    }
}

test.describe("tui demo", () => {
    test.skip(
        !CAST_FILE,
        "set RUNWISP_TUI_CAST_FILE (via the demo-tui moon task) to record the TUI demo",
    );

    test("tui demo tour", async ({ page }) => {
        if (!CAST_FILE) throw new Error("RUNWISP_TUI_CAST_FILE unset");
        const cast = parseCastFile(await readFile(CAST_FILE, "utf-8"));

        await setupTerminalPage(page);
        await page.evaluate(createTerminal, TERMINAL_OPTIONS);

        // The screencast captures the whole viewport (unlike tui.screenshots.ts's
        // per-element screenshot), so shrink the viewport to the window chrome's
        // own footprint. Otherwise every captured frame carries a wide margin of
        // blank canvas to the right and bottom, out to the config's fallback size.
        const frameBox = await page.locator("#frame").boundingBox();
        if (!frameBox) throw new Error("missing #frame element");
        await page.setViewportSize({
            width: Math.ceil(frameBox.width),
            height: Math.ceil(frameBox.height),
        });

        // Let the empty terminal shell settle before capture begins, so the
        // first captured frame is the correctly-sized dark grid, not a flash
        // of the bare window chrome.
        await page.waitForTimeout(300);

        const screencast = await Screencast.start(page, FRAMES_DIR);
        await page.evaluate(replayCast, cast);

        // term.write()'s callback fires once xterm has parsed the data, not once
        // the browser has painted it. Without this settle, stop() can race the
        // final repaint, and the closing beat (fullscreen) never gets captured.
        await page.waitForTimeout(250);

        // Hold the final frame before the clip ends.
        await screencast.stop(900);
        // A silently empty capture would still "succeed" without this — catch it here.
        expect(screencast.frameCount).toBeGreaterThan(0);
        console.log(`[tui-demo] captured ${screencast.frameCount} frames -> ${FRAMES_DIR}`);
    });
});
