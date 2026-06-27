// Copyright (c) PoppyCake, s.r.o. SPDX-License-Identifier: Apache-2.0

import cronstrue from "cronstrue";

export interface HumanizedCron {
    /** Display text — humanized when possible, otherwise the raw expression. */
    humanized: string;
    /** The original cron expression, for tooltips. */
    raw: string;
    /** False when humanizing failed and `humanized` is just `raw`. */
    isHumanized: boolean;
}

const EVERY_RE = /^@every\s+(\S+)$/;

const UNIT_NAMES: Record<string, [string, string]> = {
    h: ["hour", "hours"],
    m: ["minute", "minutes"],
    s: ["second", "seconds"],
};

// humanizeEvery turns robfig's `@every 1h30m` (Go duration syntax) into
// "Every 1 hour 30 minutes". Returns null when the duration doesn't parse.
function humanizeEvery(duration: string): string | null {
    // The number is matched atomically (lookahead-capture + backreference) so a
    // long digit run with no trailing unit fails fast instead of backtracking
    // through every length (super-linear). `\1` is the number; group 2 the unit.
    const parts = [...duration.matchAll(/(?=(\d+(?:\.\d+)?))\1(h|m|s|ms|us|µs|ns)/g)];
    if (parts.length === 0 || parts.map((p) => p[0]).join("") !== duration) {
        return null;
    }

    const words: string[] = [];
    for (const [, amount, unit] of parts) {
        if (!amount || !unit) return null;
        const value = Number(amount);
        if (value === 0) continue;
        const names = UNIT_NAMES[unit];
        if (!names) {
            // Sub-second units are valid Go durations but absurd as schedules;
            // show the raw expression rather than "Every 500 milliseconds".
            return null;
        }
        words.push(`${amount} ${value === 1 ? names[0] : names[1]}`);
    }
    if (words.length === 0) return null;

    return `Every ${words.join(" ")}`;
}

/**
 * humanizeCron renders a cron expression as plain English ("Every 5 minutes").
 * Handles robfig's `@every <duration>` extension (cronstrue throws on it) and
 * falls back to the raw expression on anything unparseable — never "Invalid".
 */
export function humanizeCron(cron: string): HumanizedCron {
    const raw = cron.trim();

    const every = EVERY_RE.exec(raw);
    if (every?.[1]) {
        const humanized = humanizeEvery(every[1]);
        if (humanized) {
            return { humanized, raw, isHumanized: true };
        }
        return { humanized: raw, raw, isHumanized: false };
    }

    try {
        const humanized = cronstrue.toString(raw, { throwExceptionOnParseError: true });
        return { humanized, raw, isHumanized: true };
    } catch {
        return { humanized: raw, raw, isHumanized: false };
    }
}
