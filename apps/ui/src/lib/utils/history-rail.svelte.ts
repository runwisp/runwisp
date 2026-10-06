// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { MediaQuery } from "svelte/reactivity";
import { StoredFlag } from "./stored-flag.svelte";

/** Tailwind's `max-md`: below this the run list and run detail can't sit side by side. */
const PHONE_QUERY = "(width < 48rem)";
/** Tailwind's `3xl` (theme.css): from here the run list always stays on screen. */
const WIDE_QUERY = "(width >= 100rem)";
const LIST_HIDDEN_KEY = "runwisp:run-list-hidden";

type Flag = { current: boolean };

/**
 * Which of the run history list and a run's detail are on screen.
 *
 * A very wide screen shows both. A mid-sized one shows both too, but the
 * operator can fold the list away to give the log more room (remembered across
 * reloads). A phone fits one: the list until a run is picked, then that run
 * until the detail's back button returns to the list.
 */
export class HistoryRail {
    #showingRun = $state(false);
    readonly #phone: { readonly current: boolean };
    readonly #wide: { readonly current: boolean };
    readonly #listHidden: Flag;

    constructor(
        runLinked = false,
        phone: { readonly current: boolean } = new MediaQuery(PHONE_QUERY),
        wide: { readonly current: boolean } = new MediaQuery(WIDE_QUERY),
        listHidden: Flag = new StoredFlag(LIST_HIDDEN_KEY),
    ) {
        this.#phone = phone;
        this.#wide = wide;
        this.#listHidden = listHidden;
        this.reset(runLinked);
    }

    /** True when only one pane fits, so the detail needs a way back to the list. */
    get phone(): boolean {
        return this.#phone.current;
    }

    /** True when both panes fit but the list may be folded away. */
    get collapsible(): boolean {
        return !this.#phone.current && !this.#wide.current;
    }

    /** @param runLinked a run is already picked by the URL, so a phone opens on it. */
    reset(runLinked: boolean): void {
        this.#showingRun = runLinked;
    }

    /** A run was picked from the list: on a phone, show it in place of the list. */
    picked = (): void => {
        this.#showingRun = true;
    };

    back = (): void => {
        this.#showingRun = false;
    };

    toggleList = (): void => {
        this.#listHidden.current = !this.#listHidden.current;
    };

    /**
     * The header search ran: bring the list back so its results are on screen.
     * An empty query is skipped, since the header dispatches one as soon as a
     * page registers, and that must not pull a phone off a deep-linked run.
     */
    searched = (query: string): void => {
        if (!query) return;
        this.#showingRun = false;
        this.#listHidden.current = false;
    };

    /**
     * Which panes to render. `hasRun`: the detail panel has a run (and so the
     * back button). `empty`: the list loaded with no runs, so the detail's
     * empty state (with its Run button) is the useful pane on a phone.
     */
    panes(hasRun: boolean, empty: boolean): { list: boolean; detail: boolean } {
        if (this.#phone.current) {
            const detail = empty || (hasRun && this.#showingRun);
            return { list: !detail, detail };
        }
        // Without a run the detail has no header to unfold the list from.
        const list = !this.collapsible || !this.#listHidden.current || !hasRun;
        return { list, detail: true };
    }
}
