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
