// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

export const ACCORDION_KEY = Symbol("accordion");

// Shared by an Accordion and its items. In single mode an item records itself
// as `active` when it opens, and every other item closes in response.
export class AccordionGroup {
    active = $state<string | null>(null);
    readonly #isSingle: () => boolean;

    constructor(isSingle: () => boolean) {
        this.#isSingle = isSingle;
    }

    get single(): boolean {
        return this.#isSingle();
    }
}
