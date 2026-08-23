// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from "vitest";
import type { Notification } from "$lib/stores";
import { hasUnreadError } from "./notification-bell.js";

function notification(overrides: Partial<Notification> = {}): Notification {
    return {
        id: "n1",
        fingerprint: "fp1",
        kind: "task_failed",
        severity: "info",
        taskName: "",
        runId: "",
        title: "",
        body: "",
        count: 1,
        occurrences: [],
        createdAt: "2026-01-01T00:00:00Z",
        lastOccurredAt: "2026-01-01T00:00:00Z",
        readAt: null,
        ...overrides,
    };
}

describe("hasUnreadError", () => {
    it("is false with no notifications", () => {
        expect(hasUnreadError([])).toBe(false);
    });

    it("is false when unread notifications exist but none are errors", () => {
        // Regression: several unread info-level notifications must not trigger
        // the bell's error treatment.
        const items = [
            notification({ id: "1", severity: "info", readAt: null }),
            notification({ id: "2", severity: "warn", readAt: null }),
        ];
        expect(hasUnreadError(items)).toBe(false);
    });

    it("is false when an error notification exists but it's already read", () => {
        // Regression: a read error notification sitting among unread
        // info-level ones must not turn the bell red.
        const items = [
            notification({ id: "1", severity: "error", readAt: "2026-01-01T00:00:01Z" }),
            notification({ id: "2", severity: "info", readAt: null }),
            notification({ id: "3", severity: "info", readAt: null }),
        ];
        expect(hasUnreadError(items)).toBe(false);
    });

    it("is true when an unread error notification exists", () => {
        const items = [
            notification({ id: "1", severity: "info", readAt: null }),
            notification({ id: "2", severity: "error", readAt: null }),
        ];
        expect(hasUnreadError(items)).toBe(true);
    });
});
