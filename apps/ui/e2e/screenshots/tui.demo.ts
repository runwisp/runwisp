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
// (screencast.ts). The key presses and mouse events the harness sent are
// replayed on top as a pointer cursor with click ripples and a key chip, so the
// clip shows what is being pressed or clicked. scripts/encode-demo-video.sh
// turns the frames into the committed animated WebP. Run via `bun run demo-tui`; not part of
// `bun run ci`.

import { expect, test } from "@playwright/test";
import { readFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { COLS, ROWS, TERMINAL_OPTIONS, setupTerminalPage } from "./tui-harness";
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

interface InputEntry {
    t: number;
    k: string;
}

interface CastFile {
    markTime: number;
    entries: CastChunk[];
    keys: InputEntry[];
}

// One harness input, classified for the browser-side overlay: a pointer event
// at a terminal cell (0-based) or a key/scroll chip label.
type OverlayEvent =
    | { at: number; kind: "move" | "down" | "up"; col: number; row: number }
    | { at: number; kind: "chip"; label: string; merge: boolean };

const KEY_LABELS: Record<string, string> = {
    "\r": "enter",
    "\x1b": "esc",
    "\x1b[A": "↑",
    "\x1b[B": "↓",
    "\x1b[C": "→",
    "\x1b[D": "←",
};

const SGR_MOUSE_PREFIX = "\x1b[<";
const MOUSE_WHEEL_UP = 64;
const MOUSE_WHEEL_DOWN = 65;
const MOUSE_LEFT = 0;

// Turns one raw PTY input (an SGR mouse report or a key sequence) into an
// overlay event, or null for input with nothing to show.
export function classifyInput(at: number, k: string): OverlayEvent | null {
    if (k.startsWith(SGR_MOUSE_PREFIX)) {
        const release = k.endsWith("m");
        const [code, x, y] = k.slice(SGR_MOUSE_PREFIX.length, -1).split(";").map(Number);
        const col = x - 1;
        const row = y - 1;
        if (code === MOUSE_WHEEL_UP || code === MOUSE_WHEEL_DOWN) {
            const label = code === MOUSE_WHEEL_UP ? "scroll ↑" : "scroll ↓";
            return { at, kind: "chip", label, merge: false };
        }
        if (code === MOUSE_LEFT) return { at, kind: release ? "up" : "down", col, row };
        return { at, kind: "move", col, row };
    }
    const named = KEY_LABELS[k];
    if (named) return { at, kind: "chip", label: named, merge: false };
    return { at, kind: "chip", label: k, merge: true };
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

    const rawKeys: unknown[] = "keys" in data && Array.isArray(data.keys) ? data.keys : [];
    const keys = rawKeys.map((entry, index) => {
        if (
            typeof entry !== "object" ||
            entry === null ||
            !("t" in entry) ||
            !("k" in entry) ||
            typeof entry.t !== "number" ||
            typeof entry.k !== "string"
        ) {
            throw new Error(`invalid TUI cast file: bad key entry at index ${index}`);
        }
        return { t: entry.t, k: entry.k };
    });

    return { markTime: data.markTime, entries, keys };
}

interface OverlayState {
    cursor: HTMLElement;
    chip: HTMLElement;
    layer: HTMLElement;
    originX: number;
    originY: number;
    cellW: number;
    cellH: number;
}

declare global {
    interface Window {
        __tuiTerm?: import("@xterm/xterm").Terminal;
        __overlay?: OverlayState;
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

// Runs in the browser, after the terminal is open: adds the pointer cursor and
// key chip, absolutely positioned over the xterm grid. Cell size comes from the
// rendered grid, so the pointer lands on the same cell the TUI saw.
async function createOverlay(grid: { cols: number; rows: number }): Promise<void> {
    const mount = document.getElementById("term");
    const screen = mount?.querySelector<HTMLElement>(".xterm-screen");
    if (!mount || !screen) throw new Error("missing terminal screen");
    mount.style.position = "relative";

    const layer = document.createElement("div");
    layer.id = "overlay";
    layer.style.cssText =
        "position:absolute;left:0;top:0;width:100%;height:100%;pointer-events:none;overflow:hidden;";
    mount.appendChild(layer);

    const cursor = document.createElement("div");
    cursor.id = "pointer";
    cursor.style.cssText =
        "position:absolute;left:0;top:0;width:20px;height:20px;opacity:0;transition:opacity 200ms;";
    cursor.innerHTML =
        '<svg width="20" height="20" viewBox="0 0 20 20"><path d="M2 1 L2 15 L6 11.5 L9 18 L11.5 17 L8.7 10.6 L14 10.6 Z" fill="#ffffff" stroke="#0c1719" stroke-width="1.2" stroke-linejoin="round"/></svg>';
    layer.appendChild(cursor);

    const chip = document.createElement("div");
    chip.id = "chip";
    chip.style.cssText =
        "position:absolute;right:18px;bottom:18px;opacity:0;transition:opacity 150ms;" +
        "padding:6px 12px;background:#0c1719;color:#2fd4dd;border:1px solid #2fd4dd;" +
        "border-radius:4px;font:600 15px 'Geist Mono Variable',monospace;";
    layer.appendChild(chip);

    const rect = screen.getBoundingClientRect();
    const mountRect = mount.getBoundingClientRect();
    window.__overlay = {
        cursor,
        chip,
        layer,
        originX: rect.left - mountRect.left,
        originY: rect.top - mountRect.top,
        cellW: rect.width / grid.cols,
        cellH: rect.height / grid.rows,
    };
}

// Runs in the browser, alongside replayCast: plays the classified inputs on the
// same clock (relative to markTime), moving the pointer, pulsing a ripple on
// click, and flashing the key chip.
async function playOverlay(args: { events: OverlayEvent[]; markTime: number }): Promise<void> {
    const state = window.__overlay;
    if (!state) throw new Error("overlay not initialized");
    const { cursor, chip, layer, originX, originY, cellW, cellH } = state;

    let chipTimer = 0;
    let lastChipAt = -Infinity;
    const showChip = (label: string, merge: boolean, at: number) => {
        const extend = merge && at - lastChipAt < 0.45 && chip.dataset.merge === "1";
        chip.textContent = extend ? `${chip.textContent ?? ""}${label}` : label;
        chip.dataset.merge = merge ? "1" : "0";
        chip.style.opacity = "1";
        lastChipAt = at;
        window.clearTimeout(chipTimer);
        chipTimer = window.setTimeout(() => {
            chip.style.opacity = "0";
        }, 900);
    };

    const place = (col: number, row: number) => {
        // Point at the cell's left third, vertically centred: a real pointer tip.
        const x = originX + col * cellW + cellW * 0.3;
        const y = originY + row * cellH + cellH * 0.2;
        cursor.style.transform = `translate(${x}px, ${y}px)`;
        cursor.style.opacity = "1";
        return { x, y };
    };

    const ripple = (x: number, y: number) => {
        const ring = document.createElement("div");
        ring.style.cssText =
            "position:absolute;width:36px;height:36px;border-radius:50%;border:2px solid #2fd4dd;" +
            `left:${x - 18}px;top:${y - 18}px;`;
        layer.appendChild(ring);
        ring.animate(
            [
                { transform: "scale(0.3)", opacity: 0.95 },
                { transform: "scale(1.5)", opacity: 0 },
            ],
            { duration: 450, easing: "ease-out" },
        ).finished.then(() => ring.remove());
    };

    const started = performance.now();
    for (const ev of args.events) {
        const wait = (ev.at - args.markTime) * 1000 - (performance.now() - started);
        if (wait > 0) await new Promise((res) => setTimeout(res, wait));
        if (ev.kind === "chip") {
            showChip(ev.label, ev.merge, ev.at);
        } else {
            const { x, y } = place(ev.col, ev.row);
            if (ev.kind === "down") ripple(x + 4, y + 4);
        }
    }
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

        await page.evaluate(createOverlay, { cols: COLS, rows: ROWS });
        const events = cast.keys.flatMap((key) => {
            const event = classifyInput(key.t, key.k);
            return event && key.t >= cast.markTime ? [event] : [];
        });

        const screencast = await Screencast.start(page, FRAMES_DIR);
        await Promise.all([
            page.evaluate(replayCast, cast),
            page.evaluate(playOverlay, { events, markTime: cast.markTime }),
        ]);

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
