// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { formatShortId } from "./id.js";

describe("formatShortId", () => {
    it("returns the last 8 characters by default", () => {
        expect(formatShortId("01HVZ4XYZ123456ABCDEF")).toBe("56ABCDEF");
    });

    it("honours a custom length", () => {
        expect(formatShortId("01HVZ4XYZ123456ABCDEF", 4)).toBe("CDEF");
    });

    it("returns the whole string when shorter than the requested length", () => {
        expect(formatShortId("abc", 8)).toBe("abc");
    });

    it("returns an empty string for an empty input", () => {
        expect(formatShortId("")).toBe("");
    });
});
