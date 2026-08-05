// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Lossless frame capture for the demo-video recording.
//
// Playwright's built-in video is VP8 .webm — lossy, with no quality knob. Piping
// that into an animated WebP means *two* lossy generations, and the text turns
// mushy. Instead we drive Chrome's DevTools screencast (Page.startScreencast with
// format:"png") and persist every painted frame as a lossless PNG, tagged with
// its swap timestamp. scripts/encode-demo-video.sh then builds the committed
// WebP + MP4 from those PNGs in a single generation.
//
// Two extra wins over Playwright video:
//   • PNG frames are lossless, so the WebP/MP4 are a single lossy generation
//     off a supersampled render (deviceScaleFactor:2) — crisp text, no VP8 mush.
//   • Capture starts *after* the opening overview has painted, so the "Connecting…"
//     load flash never lands in the clip — the first frame is the loop anchor.
//
// The PNGs live under the gitignored test-results/ dir; only the encoded assets
// are committed.

import { type Page, type CDPSession } from "@playwright/test";
import { mkdir, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";

interface ScreencastFrame {
    file: string;
    // Frame swap time in seconds (CDP metadata.timestamp); used to derive the
    // per-frame display duration for the ffmpeg concat demuxer.
    ts: number;
}

interface ScreencastFrameEvent {
    data: string;
    sessionId: number;
    metadata: { timestamp?: number };
}

/**
 * A DevTools screencast that streams painted PNG frames to disk. One per page.
 * `start()` after the scene you want to open on has painted; `stop()` writes an
 * ffmpeg concat list (frames.txt) alongside the PNGs.
 */
export class Screencast {
    private readonly frames: ScreencastFrame[] = [];
    private readonly pending: Promise<void>[] = [];
    private index = 0;

    private constructor(
        private readonly client: CDPSession,
        private readonly dir: string,
    ) {}

    /** Begin capturing painted frames into `dir` (wiped first). */
    static async start(page: Page, dir: string): Promise<Screencast> {
        await rm(dir, { recursive: true, force: true });
        await mkdir(dir, { recursive: true });

        const client = await page.context().newCDPSession(page);
        const sc = new Screencast(client, dir);

        client.on("Page.screencastFrame", (params: ScreencastFrameEvent) => {
            const idx = sc.index++;
            const file = `f_${String(idx).padStart(5, "0")}.png`;
            // Prefer the real swap timestamp; fall back to a synthetic 24fps clock
            // if a frame arrives without one (rare) so durations stay monotonic.
            const ts =
                typeof params.metadata.timestamp === "number"
                    ? params.metadata.timestamp
                    : idx / 24;
            sc.frames.push({ file, ts });
            sc.pending.push(writeFile(join(dir, file), Buffer.from(params.data, "base64")));
            // Ack immediately (persist happens async) to keep frames flowing at
            // the compositor's rate — a slow ack throttles the screencast.
            void client.send("Page.screencastFrameAck", { sessionId: params.sessionId }).catch(() => {});
        });

        await client.send("Page.startScreencast", { format: "png", everyNthFrame: 1 });
        return sc;
    }

    /**
     * Stop capturing and write the ffmpeg concat list. The final frame is held
     * for `endHoldMs` so the closing overview lingers before the loop wraps.
     */
    async stop(endHoldMs = 900): Promise<void> {
        await this.client.send("Page.stopScreencast").catch(() => {});
        await Promise.all(this.pending);

        const lines: string[] = [];
        for (let i = 0; i < this.frames.length; i++) {
            const cur = this.frames[i];
            const next = this.frames[i + 1];
            // A frame displays until the next one is painted; the last frame holds
            // for the closing beat. Clamp tiny gaps so ffmpeg never sees 0.
            const dur = next ? Math.max(0.001, next.ts - cur.ts) : endHoldMs / 1000;
            lines.push(`file '${join(this.dir, cur.file)}'`);
            lines.push(`duration ${dur.toFixed(4)}`);
        }
        // The concat demuxer ignores the last `duration` unless a trailing `file`
        // follows it — repeat the final frame so its hold is honored.
        if (this.frames.length > 0) {
            lines.push(`file '${join(this.dir, this.frames[this.frames.length - 1].file)}'`);
        }
        await writeFile(join(this.dir, "frames.txt"), lines.join("\n") + "\n");
    }

    /** Number of frames captured (for a sanity log). */
    get frameCount(): number {
        return this.frames.length;
    }
}
