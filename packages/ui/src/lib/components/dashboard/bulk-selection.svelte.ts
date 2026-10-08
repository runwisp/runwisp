// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { SvelteSet } from "svelte/reactivity";
import type { RunSelector } from "@runwisp/common";

/**
 * Which runs are ticked in the list. Two modes:
 *   1. explicit: the operator picked specific rows; `#explicit` holds them.
 *   2. all: "everything matching the current filter"; `#except` holds opt-outs.
 */
export class BulkSelection {
    #all = $state(false);
    readonly #explicit = new SvelteSet<string>();
    readonly #except = new SvelteSet<string>();

    isSelected(id: string): boolean {
        return this.#all ? !this.#except.has(id) : this.#explicit.has(id);
    }

    toggle(id: string): void {
        const ids = this.#all ? this.#except : this.#explicit;
        if (!ids.delete(id)) ids.add(id);
    }

    clear(): void {
        this.#all = false;
        this.#explicit.clear();
        this.#except.clear();
    }

    selectAll(): void {
        this.clear();
        this.#all = true;
    }

    /** Every matching run is selected, with no opt-outs. */
    get allSelected(): boolean {
        return this.#all && this.#except.size === 0;
    }

    /**
     * The server-side selector for a bulk operation. In "all" mode it targets
     * everything matching `filter`; in explicit mode just `affectedIds`.
     */
    selector(filter: NonNullable<RunSelector["filter"]>, affectedIds: string[]): RunSelector {
        if (!this.#all) return { matchAll: false, ids: affectedIds };
        return { matchAll: true, filter, exceptIds: [...this.#except] };
    }
}
