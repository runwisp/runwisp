// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// The cron questions node-cron answers with its own parser, answered by
// `runwisp sdk-cron` instead, so the grammar and the fire times are exactly the
// daemon's. Answers are cached: these are called from hot paths.

import { askCron } from "./daemon.ts";

/** node-cron's CronFieldError. field is "expression" when the fault is the expression as a whole. */
export interface CronFieldError {
    field: string;
    value?: string;
    message: string;
}

/**
 * node-cron's ParsedFields: the values each field matches, ascending. Day of
 * week runs 0-6, Sunday 0. Always numbers: RunWisp has no L, W or # tokens.
 */
export interface ParsedFields {
    second: number[];
    minute: number[];
    hour: number[];
    dayOfMonth: (number | string)[];
    month: number[];
    dayOfWeek: (number | string)[];
}

/** node-cron's DetailedValidation. fields is absent when invalid, and for `@every`, which has none. */
export interface DetailedValidation {
    valid: boolean;
    fields?: ParsedFields;
    errors: CronFieldError[];
}

// ponytail: one ask fetches this many next firings, so polling getNextRun() on
// a per-second schedule runs the binary about once a minute.
const BATCH = 60;

interface Upcoming {
    /** When this list was asked for: it holds every firing after it, up to its last. */
    from: number;
    next: Date[];
}

// ponytail: both caches live as long as the instance, keyed by expression (and
// zone); bound them if an app builds expressions on the fly.
export class CronAnswers {
    private readonly binary: string;
    private readonly explained = new Map<string, DetailedValidation>();
    private readonly upcoming = new Map<string, Upcoming>();

    constructor(binary: string) {
        this.binary = binary;
    }

    explain(expression: string): DetailedValidation {
        let found = this.explained.get(expression);
        if (!found) {
            const { valid, fields, errors } = askCron(this.binary, expression, { count: 0 });
            found = fields ? { valid, fields, errors } : { valid, errors };
            this.explained.set(expression, found);
        }
        return structuredClone(found);
    }

    /** The next count firings after now, in timezone (undefined: the host's). */
    next(
        expression: string,
        timezone: string | undefined,
        count: number,
        now = new Date(),
    ): Date[] {
        const key = `${timezone ?? ""}\n${expression}`;
        const cached = this.upcoming.get(key);
        if (cached && cached.from <= now.getTime()) {
            const left = cached.next.filter((at) => at > now);
            if (left.length >= count) return left.slice(0, count);
        }
        const { next } = askCron(this.binary, expression, {
            timezone,
            after: now,
            count: Math.max(count, BATCH),
        });
        this.upcoming.set(key, { from: now.getTime(), next });
        return next.slice(0, count);
    }

    /** Whether expression fires in date's second, in timezone. Milliseconds don't count, as in node-cron. */
    match(expression: string, timezone: string | undefined, date: Date): boolean {
        const second = Math.floor(date.getTime() / 1000) * 1000;
        const cached = this.upcoming.get(`${timezone ?? ""}\n${expression}`);
        const last = cached?.next.at(-1);
        if (cached && last && cached.from < second && second <= last.getTime()) {
            return cached.next.some((at) => at.getTime() === second);
        }
        const [next] = askCron(this.binary, expression, {
            timezone,
            after: new Date(second - 1000),
            count: 1,
        }).next;
        return next?.getTime() === second;
    }
}
