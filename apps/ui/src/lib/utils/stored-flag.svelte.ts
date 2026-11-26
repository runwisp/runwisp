// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

/** A reactive boolean the browser remembers across reloads (a layout choice). */
export class StoredFlag {
    #value = $state(false);
    readonly #key: string;

    constructor(key: string) {
        this.#key = key;
        try {
            this.#value = localStorage.getItem(key) === "1";
        } catch {
            // no storage (private mode, SSR): start unset
        }
    }

    get current(): boolean {
        return this.#value;
    }

    set current(value: boolean) {
        this.#value = value;
        try {
            if (value) localStorage.setItem(this.#key, "1");
            else localStorage.removeItem(this.#key);
        } catch {
            // not persisted; still applies for this page
        }
    }
}

/** A reactive set of strings the browser remembers across reloads (folded sections). */
export class StoredSet {
    #values = $state<string[]>([]);
    readonly #key: string;

    constructor(key: string) {
        this.#key = key;
        try {
            const parsed: unknown = JSON.parse(localStorage.getItem(key) ?? "[]");
            if (Array.isArray(parsed)) {
                this.#values = parsed.filter((v): v is string => typeof v === "string");
            }
        } catch {
            // no storage, or a corrupt value: start empty
        }
    }

    has(value: string): boolean {
        return this.#values.includes(value);
    }

    toggle(value: string): void {
        const next = this.has(value)
            ? this.#values.filter((v) => v !== value)
            : [...this.#values, value];
        this.#values = next;
        try {
            if (next.length > 0) localStorage.setItem(this.#key, JSON.stringify(next));
            else localStorage.removeItem(this.#key);
        } catch {
            // not persisted; still applies for this page
        }
    }
}
