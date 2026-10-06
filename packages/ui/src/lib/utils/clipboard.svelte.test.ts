// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { CopyFeedback, copyText } from "./clipboard.svelte.js";

describe("copyText", () => {
    afterEach(() => {
        vi.unstubAllGlobals();
    });

    it("writes through navigator.clipboard", async () => {
        const writeText = vi.fn(() => Promise.resolve());
        vi.stubGlobal("navigator", { clipboard: { writeText } });
        expect(await copyText("runwisp")).toBe(true);
        expect(writeText).toHaveBeenCalledWith("runwisp");
    });

    it("falls back to execCommand when the clipboard API is missing", async () => {
        vi.stubGlobal("navigator", {});
        const textarea = {
            value: "",
            readOnly: false,
            style: {},
            select: vi.fn(),
            remove: vi.fn(),
        };
        const execCommand = vi.fn(() => true);
        class FakeElement {
            focus = vi.fn();
        }
        const focused = new FakeElement();
        vi.stubGlobal("HTMLElement", FakeElement);
        vi.stubGlobal("document", {
            activeElement: focused,
            createElement: () => textarea,
            body: { appendChild: vi.fn() },
            execCommand,
        });
        expect(await copyText("runwisp")).toBe(true);
        expect(textarea.value).toBe("runwisp");
        expect(execCommand).toHaveBeenCalledWith("copy");
        expect(textarea.remove).toHaveBeenCalled();
        expect(focused.focus).toHaveBeenCalled();
    });

    it("reports failure when no copy path exists", async () => {
        vi.stubGlobal("navigator", {});
        vi.stubGlobal("document", undefined);
        expect(await copyText("runwisp")).toBe(false);
    });
});

describe("CopyFeedback", () => {
    beforeEach(() => {
        vi.useFakeTimers();
        vi.stubGlobal("navigator", { clipboard: { writeText: () => Promise.resolve() } });
    });
    afterEach(() => {
        vi.useRealTimers();
        vi.unstubAllGlobals();
    });

    it("flags the copied key until the reset delay passes", async () => {
        const feedback = new CopyFeedback(1000);
        await feedback.copy("docker run", "docker");
        expect(feedback.copied).toBe(true);
        expect(feedback.isCopied("docker")).toBe(true);
        expect(feedback.isCopied("binary")).toBe(false);
        vi.advanceTimersByTime(1000);
        expect(feedback.copied).toBe(false);
    });

    it("restarts the reset delay on a second copy", async () => {
        const feedback = new CopyFeedback(1000);
        await feedback.copy("a");
        vi.advanceTimersByTime(800);
        await feedback.copy("b");
        vi.advanceTimersByTime(800);
        expect(feedback.copied).toBe(true);
    });

    it("stays idle when the copy fails", async () => {
        vi.stubGlobal("navigator", {
            clipboard: { writeText: () => Promise.reject(new Error("denied")) },
        });
        vi.stubGlobal("document", undefined);
        const feedback = new CopyFeedback();
        expect(await feedback.copy("a")).toBe(false);
        expect(feedback.copied).toBe(false);
    });
});
