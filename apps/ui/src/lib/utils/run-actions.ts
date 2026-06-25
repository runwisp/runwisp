// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import type { Run, RunSelector } from "@runwisp/common";
import { toast, extractErrorMessage } from "@runwisp/ui";
import { runsApi } from "$lib/api";

/** Window in which a destructive action's toast offers an Undo. */
const UNDO_MS = 5000;

interface TriggeredRun {
    task_name: string;
    run_id: string;
}

export interface RunActionsOptions {
    /** The current run list, read fresh at call time (snapshots, lookups). */
    getItems: () => Run[];
    /** Splice runs out of the local list immediately (optimistic). */
    onOptimisticRemove: (ids: string[]) => void;
    /** Splice runs back in — undo, or rollback after a failed delete. */
    onOptimisticRestore: (runs: Run[]) => void;
    /** Notified with the removed ids so a page can drop a now-stale selection. */
    onRemoved?: (removedIds: Set<string>) => void;
}

/**
 * Bulk run operations (delete/cancel/re-run) with optimistic local updates,
 * undo toasts, and error rollback — identical across the cross-task /runs view
 * and a task's detail page, so it lives here once. The caller owns the run list
 * and selection; this owns the API calls and the toast choreography.
 */
export function createRunActions(opts: RunActionsOptions) {
    async function handleBulkDelete(selector: RunSelector, affected: Run[]) {
        if (affected.length === 0) return;
        const removedIds = new Set(affected.map((r) => r.id));
        const snapshot = opts.getItems().filter((r) => removedIds.has(r.id));
        opts.onOptimisticRemove([...removedIds]);
        opts.onRemoved?.(removedIds);

        try {
            const count = await runsApi.bulkDelete(selector);
            const restoreSelector: RunSelector = { match_all: false, ids: [...removedIds] };
            toast.success(count === 1 ? "Run deleted" : `${String(count)} runs deleted`, {
                duration: UNDO_MS,
                action: {
                    label: "Undo",
                    onClick: () => void undoDelete(restoreSelector, snapshot),
                },
            });
        } catch (err) {
            opts.onOptimisticRestore(snapshot);
            toast.error(extractErrorMessage(err, "Failed to delete runs"));
        }
    }

    async function undoDelete(selector: RunSelector, snapshot: Run[]) {
        try {
            await runsApi.bulkRestore(selector);
            // Restore optimistically — SSE run.updated will also splice them
            // back in if not already.
            opts.onOptimisticRestore(snapshot);
        } catch (err) {
            toast.error(extractErrorMessage(err, "Failed to restore runs"));
        }
    }

    async function handleBulkCancel(selector: RunSelector, affected: Run[]) {
        if (affected.length === 0) return;
        try {
            const count = await runsApi.bulkCancel(selector);
            toast.success(count === 1 ? "Cancelled 1 run" : `Cancelled ${String(count)} runs`);
        } catch (err) {
            toast.error(extractErrorMessage(err, "Failed to cancel runs"));
        }
    }

    async function handleBulkRerun(selector: RunSelector, _affected: Run[]) {
        try {
            const { triggered } = await runsApi.bulkRerun(selector);
            if (triggered.length === 0) {
                toast.error("Could not re-run any of the selected tasks");
                return;
            }
            const label = triggered.length === 1 ? "task" : "tasks";
            toast.success(`Triggered ${String(triggered.length)} ${label}`, {
                duration: UNDO_MS,
                action: {
                    label: "Undo",
                    onClick: () => void undoRerun(triggered),
                },
            });
        } catch (err) {
            toast.error(extractErrorMessage(err, "Failed to re-run tasks"));
        }
    }

    async function undoRerun(triggered: TriggeredRun[]) {
        const ids = triggered.map((t) => t.run_id);
        try {
            await runsApi.bulkCancel({ match_all: false, ids });
        } catch {
            // best-effort: runs may already have finished
        }
        try {
            await runsApi.bulkDelete({ match_all: false, ids });
            toast.info("Re-run undone");
        } catch (err) {
            toast.error(extractErrorMessage(err, "Failed to undo re-run"));
        }
    }

    function deleteSingle(runId: string) {
        const target = opts.getItems().find((r) => r.id === runId);
        if (!target) return;
        void handleBulkDelete({ match_all: false, ids: [runId] }, [target]);
    }

    return { handleBulkDelete, handleBulkCancel, handleBulkRerun, deleteSingle };
}
