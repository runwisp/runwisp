// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { resolveTrapFocusTarget } from "./focusTrap.js";

// resolveTrapFocusTarget is generic and identity-based on purpose: it never
// touches the DOM, so it's exercised here with plain sentinel objects
// instead of real elements (this repo's vitest env has no jsdom).
describe("resolveTrapFocusTarget", () => {
    const first = { id: "first" };
    const middle = { id: "middle" };
    const last = { id: "last" };
    const focusable = [first, middle, last];

    it("wraps Tab from the last focusable element back to the first", () => {
        const target = resolveTrapFocusTarget({ key: "Tab", shiftKey: false }, focusable, last);
        expect(target).toBe(first);
    });

    it("wraps Shift+Tab from the first focusable element back to the last", () => {
        const target = resolveTrapFocusTarget({ key: "Tab", shiftKey: true }, focusable, first);
        expect(target).toBe(last);
    });

    it("does nothing when Tab is pressed from a middle element", () => {
        const target = resolveTrapFocusTarget({ key: "Tab", shiftKey: false }, focusable, middle);
        expect(target).toBeNull();
    });

    it("does nothing when Shift+Tab is pressed from a middle element", () => {
        const target = resolveTrapFocusTarget({ key: "Tab", shiftKey: true }, focusable, middle);
        expect(target).toBeNull();
    });

    it("ignores non-Tab keys", () => {
        const target = resolveTrapFocusTarget({ key: "Escape", shiftKey: false }, focusable, last);
        expect(target).toBeNull();
    });

    it("does nothing when there are no focusable elements", () => {
        const target = resolveTrapFocusTarget({ key: "Tab", shiftKey: false }, [], null);
        expect(target).toBeNull();
    });

    it("wraps a single focusable element to itself in both directions", () => {
        const solo = [first];
        expect(resolveTrapFocusTarget({ key: "Tab", shiftKey: false }, solo, first)).toBe(first);
        expect(resolveTrapFocusTarget({ key: "Tab", shiftKey: true }, solo, first)).toBe(first);
    });

    it("does nothing when active is null and doesn't match first or last", () => {
        const target = resolveTrapFocusTarget({ key: "Tab", shiftKey: false }, focusable, null);
        expect(target).toBeNull();
    });
});
