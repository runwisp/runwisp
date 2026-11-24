// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { stripAnsi } from "./ansi.js";
import type { LogSlice } from "./types.js";
import { extractErrorMessage } from "../utils/error.js";

/** One page request against the run's log search (the daemon's `runId`-scoped search). */
export type FindQuery = {
    q: string;
    regex: boolean;
    case: boolean;
    limit: number;
    cursor?: string;
};
/** One page of matches: absolute line numbers, plus a cursor while more remain. */
export type FindPage = { items: Array<{ n: number }>; nextCursor?: string | undefined };
export type SearchLogFn = (runId: string, query: FindQuery) => Promise<FindPage>;

export interface Finder {
    /** Whether a raw log line (ANSI codes included) matches. */
    test(raw: string): boolean;
    /** [start, end) offsets of every match in already-visible text. */
    ranges(visible: string): Array<[number, number]>;
}

const PAGE_SIZE = 1000;
// ponytail: 10 pages (10k matches) per find; past that the count shows "+"
// and live matching stops rather than leave a silent gap.
const MAX_PAGES = 10;

/**
 * Compiles a find query the way the daemon reads it: a substring unless
 * `regex`, case-insensitive unless `caseSensitive`, matched against the text
 * without ANSI codes. Null for an empty query.
 */
export function compileFind(
    q: string,
    regex: boolean,
    caseSensitive: boolean,
): Finder | { error: string } | null {
    if (!q) return null;
    const source = regex ? q : q.replace(/[.*+?^${}()|[\]\\]/g, String.raw`\$&`);
    const flags = caseSensitive ? "" : "i";
    let single: RegExp;
    let global: RegExp;
    try {
        single = new RegExp(source, flags);
        global = new RegExp(source, flags + "g");
    } catch (err) {
        return { error: extractErrorMessage(err, "Invalid regular expression") };
    }
    return {
        test: (raw) => single.test(stripAnsi(raw)),
        ranges: (visible) => {
            const out: Array<[number, number]> = [];
            for (const m of visible.matchAll(global)) {
                // Zero-width matches (`^`, `a*`) have nothing to paint.
                if (m[0].length > 0) out.push([m.index, m.index + m[0].length]);
            }
            return out;
        },
    };
}

/** Merges two ascending line lists into one, dropping duplicates. */
export function mergeLines(a: readonly number[], b: readonly number[]): number[] {
    return [...a, ...b].sort((x, y) => x - y).filter((n, i, all) => i === 0 || all[i - 1] !== n);
}

/**
 * LogFind is the "find in this run's log" state behind the console's find
 * bar. The log is windowed, so matches come from the daemon's search over the
 * whole file; lines that stream in afterwards are matched here and appended.
 * The selection is a line number, so new matches never move it.
 */
export class LogFind {
    open = $state(false);
    query = $state("");
    regex = $state(false);
    caseSensitive = $state(false);
    /** Matching line numbers, ascending. */
    matches = $state.raw<number[]>([]);
    /**
     * The match to show. A fresh object per selection so the console reveals
     * it even when the line is the same as last time.
     */
    target = $state.raw<{ line: number } | null>(null);
    status = $state<"idle" | "searching" | "done" | "error">("idle");
    error = $state<string | null>(null);
    capped = $state(false);

    finder = $derived(compileFind(this.query, this.regex, this.caseSensitive));

    #seq = 0;
    readonly #searchLog: SearchLogFn;

    constructor(searchLog: SearchLogFn) {
        this.#searchLog = searchLog;
    }

    get current(): number | null {
        return this.target?.line ?? null;
    }

    /** Position of the selection in `matches`, or -1. */
    get index(): number {
        const cur = this.current;
        return cur === null ? -1 : this.matches.indexOf(cur);
    }

    get #active(): Finder | null {
        const f = this.finder;
        return this.open && f && "test" in f ? f : null;
    }

    /** Searches the run for the current query, then selects the first match. */
    async search(runId: string): Promise<void> {
        const seq = ++this.#seq;
        this.matches = [];
        this.target = null;
        this.capped = false;
        this.error = null;
        const finder = this.finder;
        if (!finder) {
            this.status = "idle";
            return;
        }
        if ("error" in finder) {
            this.status = "error";
            this.error = finder.error;
            return;
        }
        this.status = "searching";
        let paged: { lastHit: number; capped: boolean } | null;
        try {
            paged = await this.#fetchPages(runId, seq);
        } catch (err) {
            if (seq !== this.#seq) return;
            this.status = "error";
            this.error = extractErrorMessage(err, "Search failed");
            return;
        }
        if (!paged) return;
        if (paged.capped) {
            // Lines streamed in during the search may sit past a gap the cap
            // left unscanned; keep only what the search itself covered.
            const { lastHit } = paged;
            this.matches = this.matches.filter((n) => n <= lastHit);
            this.capped = true;
        }
        this.status = "done";
        this.#select(this.matches[0]);
    }

    /**
     * Pulls result pages into `matches` until the search is done or capped.
     * Null when a newer search took over meanwhile.
     */
    async #fetchPages(
        runId: string,
        seq: number,
    ): Promise<{ lastHit: number; capped: boolean } | null> {
        let cursor: string | undefined;
        let lastHit = -1;
        for (let page = 0; page < MAX_PAGES; page++) {
            const res = await this.#searchLog(runId, {
                q: this.query,
                regex: this.regex,
                case: this.caseSensitive,
                limit: PAGE_SIZE,
                ...(cursor ? { cursor } : {}),
            });
            if (seq !== this.#seq) return null;
            const lines = res.items.map((hit) => hit.n);
            lastHit = Math.max(lastHit, ...lines);
            this.matches = mergeLines(this.matches, lines);
            cursor = res.nextCursor;
            if (!cursor) break;
        }
        return { lastHit, capped: !!cursor };
    }

    /** Matches newly streamed lines; never moves the selection. */
    ingest(lines: LogSlice) {
        const finder = this.#active;
        if (!finder || this.capped || this.status === "error") return;
        const found: number[] = [];
        for (const [k, text] of Object.entries(lines)) {
            const n = Number(k);
            if (Number.isFinite(n) && n >= 0 && finder.test(text)) found.push(n);
        }
        if (found.length > 0)
            this.matches = mergeLines(
                this.matches,
                found.toSorted((a, b) => a - b),
            );
    }

    next() {
        const cur = this.current;
        this.#select(
            cur === null ? this.matches[0] : (this.matches.find((n) => n > cur) ?? this.matches[0]),
        );
    }

    prev() {
        const cur = this.current;
        this.#select(
            cur === null
                ? this.matches.at(-1)
                : (this.matches.findLast((n) => n < cur) ?? this.matches.at(-1)),
        );
    }

    first() {
        this.#select(this.matches[0]);
    }

    last() {
        this.#select(this.matches.at(-1));
    }

    /**
     * Jumps `dir` pages of matches, stopping at either end. A page is 10
     * matches or a tenth of them, whichever is more.
     */
    page(dir: 1 | -1) {
        const step = Math.max(10, Math.ceil(this.matches.length / 10));
        const at = Math.max(this.index, 0) + dir * step;
        this.#select(this.matches[Math.min(Math.max(at, 0), this.matches.length - 1)]);
    }

    /** Hides the bar and its highlights; the query stays for next time. */
    close() {
        this.#seq++;
        this.open = false;
        this.matches = [];
        this.target = null;
        this.status = "idle";
        this.error = null;
        this.capped = false;
    }

    #select(line: number | undefined) {
        this.target = line === undefined ? null : { line };
    }
}
