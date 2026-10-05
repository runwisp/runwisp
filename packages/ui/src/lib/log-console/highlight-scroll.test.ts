// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it, vi } from "vitest";
import { createHighlightScroll } from "./highlight-scroll.js";

// `sync` is what LogConsole calls inside its $effect. The effect re-runs it
// whenever something it READ changes, so "no layout reads after the reveal"
// (ready not consulted again) is exactly "layout changes can't re-scroll".
function setup(initial: { line: number | null; ready: boolean }) {
    const state = { ...initial };
    const ready = vi.fn(() => state.ready);
    const reveal = vi.fn();
    const clear = vi.fn();
    const sync = createHighlightScroll({ line: () => state.line, ready, reveal, clear });
    return { state, ready, reveal, clear, sync };
}

describe("createHighlightScroll", () => {
    it("reveals once and stops reading layout afterwards", () => {
        const { ready, reveal, sync } = setup({ line: 5, ready: true });
        sync();
        expect(reveal).toHaveBeenCalledExactlyOnceWith(5);
        expect(ready).toHaveBeenCalledTimes(1);

        // A running run appends lines or the pane resizes: the effect re-runs,
        // but must neither scroll again nor re-read the layout.
        sync();
        sync();
        expect(reveal).toHaveBeenCalledTimes(1);
        expect(ready).toHaveBeenCalledTimes(1);
    });

    it("waits until the line can be placed, then reveals once", () => {
        const { state, reveal, sync } = setup({ line: 5, ready: false });
        sync();
        expect(reveal).not.toHaveBeenCalled();
        state.ready = true;
        sync();
        sync();
        expect(reveal).toHaveBeenCalledExactlyOnceWith(5);
    });

    it("reveals again for a new line and after the highlight is cleared", () => {
        const { state, reveal, clear, sync } = setup({ line: 5, ready: true });
        sync();
        state.line = 9;
        sync();
        expect(reveal).toHaveBeenLastCalledWith(9);

        state.line = null;
        sync();
        expect(clear).toHaveBeenCalledOnce();

        state.line = 9;
        sync();
        expect(reveal).toHaveBeenCalledTimes(3);
    });
});
