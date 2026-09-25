// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { z } from "zod";

export const FEEDBACK_URL = "https://concierge.runwisp.com/v1/feedback";
export const REPO_URL = "https://github.com/runwisp/runwisp";

export interface Mood {
    emoji: string;
    label: string;
    rating: number;
}

export const MOODS: readonly Mood[] = [
    { emoji: "😍", label: "Love it", rating: 5 },
    { emoji: "🙂", label: "It's fine", rating: 4 },
    { emoji: "😭", label: "Frustrating", rating: 2 },
    { emoji: "🤬", label: "Hate it", rating: 1 },
];

export function isPositive(rating: number): boolean {
    return rating >= 4;
}

const HOUR = 60 * 60 * 1000;
export const MIN_UPTIME_MS = HOUR;
export const SNOOZE_MS = 30 * 24 * HOUR;

// What this browser remembers about the prompt. `submitted` means a rating
// landed, so we never ask again; `dismissedAt` is when it was last closed
// without one.
const storedStateSchema = z.object({
    submitted: z.boolean().default(false),
    dismissedAt: z.number().optional(),
});
export type StoredState = z.infer<typeof storedStateSchema>;

export function parseStoredState(raw: string | null): StoredState {
    const fallback: StoredState = { submitted: false };
    if (!raw) return fallback;
    try {
        const parsed = storedStateSchema.safeParse(JSON.parse(raw));
        return parsed.success ? parsed.data : fallback;
    } catch {
        return fallback;
    }
}

// shouldAutoOpen decides whether the card pops up on its own: never before the
// daemon has an hour of uptime, never after a rating, and after a close, at
// most once a month. The operator can always reopen it by hand.
export function shouldAutoOpen(
    state: StoredState,
    ctx: { startedAt: number; now: number; checkUpdates: boolean },
): boolean {
    if (!ctx.checkUpdates || state.submitted || ctx.startedAt === 0) return false;
    if (ctx.now - ctx.startedAt < MIN_UPTIME_MS) return false;
    if (state.dismissedAt === undefined) return true;
    return ctx.now - state.dismissedAt >= SNOOZE_MS;
}

export type FeedbackBody = { rating: number } | { message: string };
export type SendResult = { ok: true } | { ok: false; error: string };

export async function sendFeedback(
    body: FeedbackBody,
    version: string,
    fetchFn: typeof fetch = fetch,
): Promise<SendResult> {
    let res: Response;
    try {
        res = await fetchFn(FEEDBACK_URL, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ ...body, version }),
            referrerPolicy: "no-referrer",
        });
    } catch {
        return { ok: false, error: "Couldn't reach runwisp.com from this browser." };
    }
    if (res.ok) return { ok: true };
    if (res.status === 429) {
        return { ok: false, error: "That's plenty for today, thank you! Try again tomorrow." };
    }
    return { ok: false, error: `Something went wrong (HTTP ${String(res.status)}).` };
}
