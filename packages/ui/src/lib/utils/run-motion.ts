// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Long enough to span an SSE insert and the trigger's HTTP response that
// selects the new run; short enough that a later click never re-animates it.
const WINDOW_MS = 2000;

class RecentIds {
    readonly #at = new Map<string, number>();

    mark(id: string): void {
        const now = Date.now();
        for (const [known, at] of this.#at) {
            if (now - at >= WINDOW_MS) this.#at.delete(known);
        }
        this.#at.set(id, now);
    }

    has(id: string): boolean {
        const at = this.#at.get(id);
        return at !== undefined && Date.now() - at < WINDOW_MS;
    }

    clear(): void {
        this.#at.clear();
    }
}

/**
 * Remembers which runs arrived or were removed live moments ago (a trigger, a
 * scheduled firing, a run finishing, a delete), as opposed to rows that a page
 * fetch, scroll or filter change loaded or dropped, so only genuine changes
 * animate. Plain (non-reactive): it is only read at mount, unmount or
 * selection time.
 */
export class RunMotion {
    readonly #arrived = new RecentIds();
    readonly #removed = new RecentIds();

    markArrived(runId: string): void {
        this.#arrived.mark(runId);
    }

    markRemoved(runId: string): void {
        this.#removed.mark(runId);
    }

    // Arrows so they can be handed around unbound.
    arrived = (runId: string): boolean => this.#arrived.has(runId);
    removed = (runId: string): boolean => this.#removed.has(runId);

    clear(): void {
        this.#arrived.clear();
        this.#removed.clear();
    }
}
