// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { parseLogPage, type LogPage } from "./logs";

// ─── parseLogPage ─────────────────────────────────────────────────────────────

describe("parseLogPage", () => {
    const makePage = (overrides: Partial<LogPage> = {}): LogPage => ({
        lines: [],
        first_available: 0,
        total_lines: 0,
        truncated: false,
        finalized: false,
        ...overrides,
    });

    it("maps lines into a Record keyed by line number", () => {
        const page = makePage({
            lines: [
                { n: 0, stream: "stdout", text: "first" },
                { n: 1, stream: "stdout", text: "second" },
            ],
            total_lines: 2,
        });
        const evt = parseLogPage(page);
        expect(evt.lines).toEqual({ 0: "first", 1: "second" });
        expect(evt.sizeLines).toBe(2);
    });

    it("sets finished from page.finalized", () => {
        const page = makePage({ finalized: true });
        const evt = parseLogPage(page);
        expect(evt.finished).toBe(true);
    });

    it("does NOT set firstAvailableLine when first_available is 0 (zero branch)", () => {
        const page = makePage({ first_available: 0 });
        const evt = parseLogPage(page);
        expect(evt.firstAvailableLine).toBeUndefined();
    });

    it("sets firstAvailableLine when first_available > 0 (non-zero branch)", () => {
        const page = makePage({ first_available: 5 });
        const evt = parseLogPage(page);
        expect(evt.firstAvailableLine).toBe(5);
    });

    it("preserves stream and line-number gaps from sparse input", () => {
        const page = makePage({
            lines: [
                { n: 3, stream: "stderr", text: "boom" },
                { n: 7, stream: "stdout", text: "ok" },
            ],
            total_lines: 8,
        });
        const evt = parseLogPage(page);
        expect(evt.lines).toEqual({ 3: "boom", 7: "ok" });
        expect(Object.keys(evt.lines)).toHaveLength(2);
        expect(evt.sizeLines).toBe(8);
    });
});
