// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import type { Run } from "@runwisp/common";
import { createRunActions } from "./run-actions";

interface RunSelectionOptions {
    getItems: () => Run[];
    /** The deep-linked run id from the URL, if any. */
    getInitialRunId: () => string | null;
    /** The deep-linked run was fetched and doesn't exist. */
    getRunNotFound: () => boolean;
    /** The deep-linked run is still being fetched. */
    getRunPending: () => boolean;
    onOptimisticRemove: (ids: string[]) => void;
    onOptimisticRestore: (runs: Run[]) => void;
    /** With no explicit pick, fall back to a running run before the newest. */
    preferRunning?: boolean;
    /** A run picked from outside the list (e.g. one just triggered). */
    getSelectRunId?: () => string | null;
    /** Called when a deep link or outside pick seeds the selection. */
    onSeeded: () => void;
    /** Reports explicit selections upward so the URL can mirror the run. */
    onSelectRun?: ((runId: string | null) => void) | undefined;
}

/**
 * Run selection shared by the /runs view and a task's page, plus the bulk run
 * actions (which drop a selection whose run was deleted). Seeds the selection
 * from the deep link and reports it upward, so it must be called during
 * component init (it registers effects).
 */
export function createRunSelection(opts: RunSelectionOptions) {
    let userSelectedRunId = $state<string | null>(null);

    // The deep-linked run genuinely doesn't exist: its id is the current URL
    // selection, the fetch confirmed it missing, and it isn't in the list. Show
    // a "not found" panel rather than silently falling back to another run
    // while the URL still points at the dead id.
    const deepLinkMissing = $derived(
        opts.getRunNotFound() &&
            userSelectedRunId !== null &&
            userSelectedRunId === opts.getInitialRunId() &&
            !opts.getItems().some((r) => r.id === userSelectedRunId),
    );

    const deepLinkPending = $derived(
        opts.getRunPending() && userSelectedRunId === opts.getInitialRunId(),
    );

    const selectedRunId = $derived.by(() => {
        const items = opts.getItems();
        if (userSelectedRunId && items.some((r) => r.id === userSelectedRunId)) {
            return userSelectedRunId;
        }
        if (deepLinkMissing || deepLinkPending) return null;
        if (opts.preferRunning === true) {
            const running = items.find((r) => r.status === "running");
            if (running) return running.id;
        }
        return items[0]?.id ?? null;
    });

    const selectedRun = $derived(opts.getItems().find((r) => r.id === selectedRunId));

    // Seed from the deep link (on load and on later URL changes) and from an
    // outside pick. These must stay before the report effect: on the first
    // flush effects run in declaration order, so the selection is seeded
    // before it is reported; otherwise the initial null would clobber the link.
    $effect(() => {
        const id = opts.getInitialRunId();
        if (!id) return;
        userSelectedRunId = id;
        opts.onSeeded();
    });

    $effect(() => {
        const id = opts.getSelectRunId?.();
        if (!id) return;
        userSelectedRunId = id;
        opts.onSeeded();
    });

    $effect(() => {
        opts.onSelectRun?.(userSelectedRunId);
    });

    const actions = createRunActions({
        getItems: opts.getItems,
        onOptimisticRemove: opts.onOptimisticRemove,
        onOptimisticRestore: opts.onOptimisticRestore,
        onRemoved: (ids) => {
            if (userSelectedRunId && ids.has(userSelectedRunId)) userSelectedRunId = null;
        },
    });

    return {
        ...actions,
        get userSelectedRunId() {
            return userSelectedRunId;
        },
        set userSelectedRunId(id: string | null) {
            userSelectedRunId = id;
        },
        get deepLinkMissing() {
            return deepLinkMissing;
        },
        get deepLinkPending() {
            return deepLinkPending;
        },
        get selectedRunId() {
            return selectedRunId;
        },
        get selectedRun() {
            return selectedRun;
        },
    };
}
