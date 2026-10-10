// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import type { Snippet } from "svelte";

/**
 * The task row the top bar shows while the task page's header is folded. The
 * page owns the row (its state and actions); the top bar only places it.
 */
function createTaskBarStore() {
    let row = $state<Snippet | null>(null);
    return {
        get row(): Snippet | null {
            return row;
        },
        show(next: Snippet): void {
            row = next;
        },
        hide(): void {
            row = null;
        },
    };
}

export const taskBarStore = createTaskBarStore();
