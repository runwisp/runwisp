// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { HistoryRail } from "./history-rail.svelte";

const yes = { current: true };
const no = { current: false };

/** A rail on a screen of the given size, with a fresh (unfolded) list. */
function rail(screen: "phone" | "mid" | "wide", runLinked = false) {
    return new HistoryRail(runLinked, screen === "phone" ? yes : no, screen === "wide" ? yes : no, {
        current: false,
    });
}

describe("HistoryRail", () => {
    it("always shows both panes on a wide screen", () => {
        const r = rail("wide", true);
        expect(r.panes(true, false)).toEqual({ list: true, detail: true, strip: true });

        r.back();
        expect(r.panes(true, false)).toEqual({ list: true, detail: true, strip: true });
    });

    it("swaps the list for the picked run on a phone, and back", () => {
        const r = rail("phone");
        expect(r.panes(true, false)).toEqual({ list: true, detail: false, strip: true });

        r.picked();
        expect(r.panes(true, false)).toEqual({ list: false, detail: true, strip: false });

        r.back();
        expect(r.panes(true, false)).toEqual({ list: true, detail: false, strip: true });
    });

    it("opens a phone on a run linked from the URL", () => {
        const r = rail("phone", true);
        expect(r.panes(true, false)).toEqual({ list: false, detail: true, strip: false });
    });

    it("keeps the list on a phone while there is no run to show", () => {
        const r = rail("phone", true);
        expect(r.panes(false, false)).toEqual({ list: true, detail: false, strip: true });
    });

    it("shows the empty detail and the task strip on a phone when the task has no runs", () => {
        const r = rail("phone");
        expect(r.panes(false, true)).toEqual({ list: false, detail: true, strip: true });
    });

    it("lets a mid-sized screen fold the list to its strip", () => {
        const r = rail("mid", true);
        expect(r.collapsible).toBe(true);
        expect(r.listFolded).toBe(false);

        r.toggleList();
        expect(r.listFolded).toBe(true);
        expect(r.panes(true, false)).toEqual({ list: true, detail: true, strip: true });

        r.toggleList();
        expect(r.listFolded).toBe(false);
    });

    it("never folds the list on a wide screen", () => {
        const r = rail("wide", true);
        expect(r.collapsible).toBe(false);
        r.toggleList();
        expect(r.listFolded).toBe(false);
    });

    it("brings the list back when a search runs", () => {
        const onPhone = rail("phone", true);
        onPhone.searched("");
        expect(onPhone.panes(true, false)).toEqual({ list: false, detail: true, strip: false });
        onPhone.searched("boom");
        expect(onPhone.panes(true, false)).toEqual({ list: true, detail: false, strip: true });

        const mid = rail("mid", true);
        mid.toggleList();
        mid.searched("boom");
        expect(mid.listFolded).toBe(false);
    });
});
