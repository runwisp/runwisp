// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { extractErrorMessage } from "./error.js";

describe("extractErrorMessage", () => {
    it("returns Error.message when given an Error", () => {
        expect(extractErrorMessage(new Error("boom"))).toBe("boom");
    });

    it("returns the fallback when an Error has an empty message", () => {
        expect(extractErrorMessage(new Error(""))).toBe("An unexpected error occurred");
    });

    it("returns a string error as-is", () => {
        expect(extractErrorMessage("plain text error")).toBe("plain text error");
    });

    it("returns the fallback for an empty string", () => {
        expect(extractErrorMessage("")).toBe("An unexpected error occurred");
    });

    it("honours a custom fallback", () => {
        expect(extractErrorMessage(undefined, "custom fallback")).toBe("custom fallback");
        expect(extractErrorMessage({ unrelated: 1 }, "custom fallback")).toBe("custom fallback");
        expect(extractErrorMessage(null, "custom fallback")).toBe("custom fallback");
    });

    it("returns the default fallback for non-Error non-string values", () => {
        expect(extractErrorMessage(42)).toBe("An unexpected error occurred");
        expect(extractErrorMessage({ code: 500 })).toBe("An unexpected error occurred");
    });
});
