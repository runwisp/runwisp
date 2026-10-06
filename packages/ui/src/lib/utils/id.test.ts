// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { formatShortId } from "./id.js";

describe("formatShortId", () => {
    it("returns the last 8 characters", () => {
        expect(formatShortId("01HVZ4XYZ123456ABCDEF")).toBe("56ABCDEF");
    });

    it("returns the whole string when shorter than 8", () => {
        expect(formatShortId("abc")).toBe("abc");
    });

    it("returns an empty string for an empty input", () => {
        expect(formatShortId("")).toBe("");
    });
});
