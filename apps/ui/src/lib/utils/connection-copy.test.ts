// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { stalledCopy } from "./connection-copy";

describe("stalledCopy", () => {
    it("keeps the chip label identical in both modes", () => {
        expect(stalledCopy(true).label).toBe(stalledCopy(false).label);
    });

    it("never blames tabs when the connection is shared across tabs", () => {
        const copy = stalledCopy(true);
        for (const text of [copy.hint, copy.title, copy.heading, copy.body]) {
            expect(text.toLowerCase()).not.toContain("tab");
        }
    });

    it("blames too many tabs only in the degraded per-tab mode", () => {
        const copy = stalledCopy(false);
        expect(copy.hint.toLowerCase()).toContain("tab");
        expect(copy.body.toLowerCase()).toContain("tab");
    });
});
