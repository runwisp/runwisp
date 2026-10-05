// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { untrack } from "svelte";
import type { Run } from "@runwisp/common";

/**
 * Restores a deep-linked run (/runs/{runId}, /tasks/{name}/{runId}) that isn't
 * in the loaded list: fetches it once, upserts it when found, and latches
 * "not found" otherwise so a dead permalink shows a panel instead of quietly
 * selecting another run. Only the newest link's lookup may change state; an
 * older one finishing late is dropped.
 */
export class RunDeepLink {
    #notFound = $state(false);
    #pendingId = $state<string | null>(null);
    #checkedMissingId: string | null = null;
    readonly #fetchRun: (runId: string) => Promise<Run>;
    readonly #upsert: (run: Run) => void;

    constructor(fetchRun: (runId: string) => Promise<Run>, upsert: (run: Run) => void) {
        this.#fetchRun = fetchRun;
        this.#upsert = upsert;
    }

    /** The deep-linked run resolved to no run. */
    get notFound(): boolean {
        return this.#notFound;
    }

    /** The deep-linked run is being fetched, so callers show a loading state. */
    get pending(): boolean {
        return this.#pendingId !== null;
    }

    /** Call from an $effect with the current link and the loaded runs. */
    resolve(runId: string | null, items: readonly Run[]): void {
        if (!runId) {
            this.#notFound = false;
            this.#pendingId = null;
            this.#checkedMissingId = null;
            return;
        }
        if (items.length === 0) return;
        if (items.some((r) => r.id === runId)) {
            this.#notFound = false;
            this.#pendingId = null;
            return;
        }
        if (this.#checkedMissingId === runId) return;
        if (untrack(() => this.#pendingId) === runId) return; // already fetching
        // A new id: clear a not-found latched by a previous dead link so it
        // doesn't flash "Run not found" for this (possibly valid) run.
        this.#notFound = false;
        this.#pendingId = runId;
        void this.#lookup(runId);
    }

    async #lookup(runId: string): Promise<void> {
        let run: Run | undefined;
        try {
            run = await this.#fetchRun(runId);
        } catch {
            // Not found / not authorized is treated as missing.
        }
        if (this.#pendingId !== runId) return; // a newer link superseded this one
        this.#pendingId = null;
        if (run) {
            this.#upsert(run);
            return;
        }
        this.#checkedMissingId = runId;
        this.#notFound = true;
    }
}
