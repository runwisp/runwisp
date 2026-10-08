// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { FrameHistory } from "./frame-history.svelte.js";
import { LINE_HEIGHT } from "./wrap-layout.svelte.js";

describe("FrameHistory", () => {
    it("takes no space while closed", () => {
        const history = new FrameHistory(() => undefined);
        expect(history.blockHeight).toBe(0);
    });

    it("reserves a placeholder while frames load, then sizes to them", async () => {
        const history = new FrameHistory(() => () => Promise.resolve([["a", "b"], ["c"]]));
        const done = history.toggle(4);
        expect(history.line).toBe(4);
        expect(history.blockHeight).toBe(LINE_HEIGHT * 2);
        await done;
        // padding 16 + (label + 2 rows) + (label + 1 row) + one 6px gap.
        expect(history.blockHeight).toBe(16 + LINE_HEIGHT * 5 + 6);
    });

    it("collapses when the open line is toggled again", async () => {
        const history = new FrameHistory(() => () => Promise.resolve([["a"]]));
        await history.toggle(1);
        await history.toggle(1);
        expect(history.line).toBeNull();
        expect(history.frames).toBeNull();
    });

    it("flags a failed fetch and ignores a superseded one", async () => {
        const failing = new FrameHistory(() => () => Promise.reject(new Error("boom")));
        await failing.toggle(2);
        expect(failing.failed).toBe(true);

        let release: (frames: string[][]) => void = () => {};
        const slow = new FrameHistory(
            () => () => new Promise<string[][]>((resolve) => (release = resolve)),
        );
        const first = slow.toggle(1);
        slow.collapse();
        release([["late"]]);
        await first;
        expect(slow.frames).toBeNull();
    });
});
