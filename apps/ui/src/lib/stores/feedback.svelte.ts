// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { browser } from "$app/environment";
import { parseStoredState, shouldAutoOpen, type StoredState } from "$lib/components/feedback";

const FEEDBACK_STORAGE_KEY = "runwisp-feedback";

// feedbackStore is shared by the sidebar card and its reopen button in the
// connection footer. What the browser remembers lives in localStorage, and
// another tab can change it underneath us, so every read goes back to
// storage instead of trusting an in-memory copy; whether the card is open
// right now is per-tab.
function createFeedbackStore() {
    let open = $state(false);

    function load(): StoredState {
        return parseStoredState(browser ? localStorage.getItem(FEEDBACK_STORAGE_KEY) : null);
    }

    function persist(next: StoredState): void {
        if (browser) localStorage.setItem(FEEDBACK_STORAGE_KEY, JSON.stringify(next));
    }

    return {
        get open(): boolean {
            return open;
        },
        show(): void {
            open = true;
        },
        autoOpen(ctx: { startedAt: number; checkUpdates: boolean }): void {
            if (shouldAutoOpen(load(), { ...ctx, now: Date.now() })) open = true;
        },
        // dismiss closes the card; closing before rating pushes out the
        // month-long snooze before it's offered again.
        dismiss(): void {
            open = false;
            const stored = load();
            if (!stored.submitted) persist({ ...stored, dismissedAt: Date.now() });
        },
        markSubmitted(): void {
            persist({ ...load(), submitted: true });
        },
    };
}

export const feedbackStore = createFeedbackStore();
