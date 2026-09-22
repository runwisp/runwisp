// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { reloadSummary } from "./stale-config-banner.js";

describe("reloadSummary", () => {
    it("reports no changes when everything is empty", () => {
        expect(reloadSummary({ added: [], removed: [], changed: [] })).toBe(
            "Config reloaded — no changes",
        );
    });

    it("reports each non-empty count", () => {
        expect(reloadSummary({ added: ["a"], removed: ["b", "c"], changed: [{}] })).toBe(
            "Config reloaded: +1 added, -2 removed, ~1 changed",
        );
    });

    it("omits zero counts", () => {
        expect(reloadSummary({ added: ["a"], removed: [], changed: [] })).toBe(
            "Config reloaded: +1 added",
        );
    });
});
