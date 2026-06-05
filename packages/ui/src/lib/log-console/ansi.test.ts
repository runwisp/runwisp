// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { ansiLineToHtml } from "./ansi.js";

describe("ansiLineToHtml", () => {
    it("passes plain text through unchanged", () => {
        expect(ansiLineToHtml("hello world")).toBe("hello world");
    });

    it("maps standard foreground codes to theme tokens", () => {
        expect(ansiLineToHtml("\x1b[31merror\x1b[0m")).toBe(
            '<span style="color:var(--rw-ansi-red)">error</span>',
        );
        expect(ansiLineToHtml("\x1b[32mok\x1b[0m")).toBe(
            '<span style="color:var(--rw-ansi-green)">ok</span>',
        );
        expect(ansiLineToHtml("\x1b[36minfo\x1b[0m")).toBe(
            '<span style="color:var(--rw-ansi-cyan)">info</span>',
        );
    });

    it("maps bright foreground codes to bright theme tokens", () => {
        expect(ansiLineToHtml("\x1b[91mwarn\x1b[0m")).toBe(
            '<span style="color:var(--rw-ansi-bright-red)">warn</span>',
        );
        expect(ansiLineToHtml("\x1b[97mtext\x1b[0m")).toBe(
            '<span style="color:var(--rw-ansi-bright-white)">text</span>',
        );
    });

    it("maps background codes to theme tokens", () => {
        expect(ansiLineToHtml("\x1b[41mfail\x1b[0m")).toBe(
            '<span style="background-color:var(--rw-ansi-red)">fail</span>',
        );
    });

    it("escapes HTML in log content", () => {
        expect(ansiLineToHtml("<script>alert(1)</script>")).toBe(
            "&lt;script&gt;alert(1)&lt;/script&gt;",
        );
    });

    it("does not leak colour state between calls", () => {
        // An unterminated colour in one line must not affect the next.
        ansiLineToHtml("\x1b[31munclosed red");
        expect(ansiLineToHtml("plain")).toBe("plain");
    });
});
