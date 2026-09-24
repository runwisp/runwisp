// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { afterEach, describe, expect, it } from "vitest";
import { feedbackStore } from "./feedback.svelte";

// The store is a process-wide singleton; reset it between cases so state set
// by one test never leaks into the next.
afterEach(() => {
    feedbackStore.dismiss();
});

describe("feedbackStore", () => {
    it("starts closed", () => {
        expect(feedbackStore.open).toBe(false);
    });

    it("show opens the card", () => {
        feedbackStore.show();
        expect(feedbackStore.open).toBe(true);
    });

    it("dismiss closes the card", () => {
        feedbackStore.show();
        feedbackStore.dismiss();
        expect(feedbackStore.open).toBe(false);
    });

    it("autoOpen opens once the daemon has an hour of uptime", () => {
        feedbackStore.autoOpen({ startedAt: Date.now() - 60 * 60 * 1000, checkUpdates: true });
        expect(feedbackStore.open).toBe(true);
    });

    it("autoOpen stays closed before an hour of uptime or with check_updates off", () => {
        feedbackStore.autoOpen({ startedAt: Date.now(), checkUpdates: true });
        expect(feedbackStore.open).toBe(false);
        feedbackStore.autoOpen({ startedAt: Date.now() - 60 * 60 * 1000, checkUpdates: false });
        expect(feedbackStore.open).toBe(false);
    });

    it("markSubmitted does not throw outside the browser", () => {
        expect(() => {
            feedbackStore.markSubmitted();
        }).not.toThrow();
    });
});
