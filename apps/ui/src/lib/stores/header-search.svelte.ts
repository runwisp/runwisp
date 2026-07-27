// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * Spec a page hands to the header search field when it mounts. The page owns
 * what a query *means* (filter the run list, search log output, …); the header
 * owns the box, the focus shortcut, and the debounce.
 */
export interface HeaderSearchSpec {
    /** Placeholder shown when the box is empty. */
    placeholder: string;
    /** Called with the live query, debounced by the header field. */
    onSearch: (query: string) => void;
}

/**
 * Cross-layout search state. The search box lives in the app header (one home,
 * every page), but each page decides whether it has a search and what running
 * one does. A page calls `register` on mount and `unregister` on teardown; the
 * header renders the field only while a spec is registered.
 */
function createHeaderSearchStore() {
    let spec = $state<HeaderSearchSpec | null>(null);
    let query = $state("");
    let loading = $state(false);

    return {
        get spec(): HeaderSearchSpec | null {
            return spec;
        },
        get active(): boolean {
            return spec !== null;
        },
        get query(): string {
            return query;
        },
        get loading(): boolean {
            return loading;
        },
        register(next: HeaderSearchSpec): void {
            spec = next;
            query = "";
            loading = false;
        },
        unregister(): void {
            spec = null;
            query = "";
            loading = false;
        },
        setQuery(value: string): void {
            query = value;
        },
        clear(): void {
            query = "";
        },
        setLoading(value: boolean): void {
            loading = value;
        },
    };
}

export const headerSearchStore = createHeaderSearchStore();
