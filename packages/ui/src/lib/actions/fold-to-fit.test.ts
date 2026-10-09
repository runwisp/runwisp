// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { foldUntilFits } from "./fold-to-fit.js";

// A row of named parts with widths, folding parts to zero.
function row(width: number, parts: Record<string, number>) {
    const shown = { ...parts };
    return {
        fits: () => Object.values(shown).reduce((a, b) => a + b, 0) <= width,
        fold: (key: string) => {
            shown[key] = 0;
        },
    };
}

describe("foldUntilFits", () => {
    const order = ["id", "via", "start", "usage"];
    const parts = { verdict: 100, id: 60, via: 40, start: 80, usage: 90 };

    it("folds nothing when the row fits", () => {
        const r = row(400, parts);
        expect(foldUntilFits(order, r.fits, r.fold)).toEqual([]);
    });

    it("folds in order and stops as soon as it fits", () => {
        const r = row(280, parts);
        expect(foldUntilFits(order, r.fits, r.fold)).toEqual(["id", "via"]);
    });

    it("folds everything foldable when even that doesn't fit", () => {
        const r = row(50, parts);
        expect(foldUntilFits(order, r.fits, r.fold)).toEqual(order);
    });
});
