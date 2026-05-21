<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { Trash2 } from "@lucide/svelte";
    import { SvelteSet } from "svelte/reactivity";
    import type { Run } from "@runwisp/common";
    import type { LogEvent, LogSlice } from "@runwisp/ui";
    import PageContainer from "@runwisp/ui/components/PageContainer.svelte";
    import Button from "@runwisp/ui/components/Button.svelte";
    import Modal from "@runwisp/ui/components/Modal.svelte";
    import { RunsList, RunDetailPanel, toast, extractErrorMessage } from "@runwisp/ui";
    import { sortByCreatedAtDesc } from "$lib/utils/sort";
    import { tasksApi } from "$lib/api";

    let {
        runs = $bindable([]),
        fetchLogs,
        streamLogs,
    } = $props<{
        runs: Run[];
        fetchLogs: (
            runId: string,
            from: number,
            to: number,
        ) => Promise<LogSlice | LogEvent | void> | LogSlice | LogEvent | void;
        streamLogs?: (
            runId: string,
            onEvent: (event: LogEvent) => void,
            initialState?: { fromLine: number },
        ) => () => void;
    }>();

    let userSelectedRunId = $state<string | null>(null);
    let deleteTargetId = $state<string | null>(null);
    let deleteConfirmOpen = $state(false);
    let deleting = $state(false);

    function requestDelete(runId: string) {
        deleteTargetId = runId;
        deleteConfirmOpen = true;
    }

    async function confirmDelete() {
        const id = deleteTargetId;
        if (!id) return;
        const target = runs.find((r: Run) => r.id === id);
        if (!target) {
            deleteConfirmOpen = false;
            deleteTargetId = null;
            return;
        }
        deleting = true;
        try {
            await tasksApi.deleteRun(target.task_name, id);
            runs = runs.filter((r: Run) => r.id !== id);
            if (userSelectedRunId === id) userSelectedRunId = null;
            toast.success("Run deleted");
        } catch (err) {
            toast.error(extractErrorMessage(err, "Failed to delete run"));
        } finally {
            deleting = false;
            deleteConfirmOpen = false;
            deleteTargetId = null;
        }
    }

    function runsByIds(ids: string[]): Run[] {
        const want = new SvelteSet(ids);
        return runs.filter((r: Run) => want.has(r.id));
    }

    async function handleBulkCancel(ids: string[]) {
        const targets = runsByIds(ids).filter((r) => r.status === "running");
        if (targets.length === 0) {
            toast.error("No running runs selected");
            return;
        }
        const results = await Promise.allSettled(
            targets.map((r) => tasksApi.stopRun(r.task_name, r.id)),
        );
        const ok = results.filter((r) => r.status === "fulfilled").length;
        if (ok === targets.length) toast.success(`Cancelled ${ok} run${ok === 1 ? "" : "s"}`);
        else toast.error(`Cancelled ${ok} / ${targets.length} runs`);
    }

    async function handleBulkDelete(ids: string[]) {
        const targets = runsByIds(ids).filter(
            (r) => r.status !== "running" && r.status !== "pending",
        );
        if (targets.length === 0) {
            toast.error("No deletable runs selected");
            return;
        }
        const results = await Promise.allSettled(
            targets.map((r) => tasksApi.deleteRun(r.task_name, r.id)),
        );
        const deleted = new SvelteSet<string>();
        results.forEach((res, idx) => {
            const target = targets[idx];
            if (res.status === "fulfilled" && target) deleted.add(target.id);
        });
        if (deleted.size > 0) {
            runs = runs.filter((r: Run) => !deleted.has(r.id));
            if (userSelectedRunId && deleted.has(userSelectedRunId)) userSelectedRunId = null;
        }
        if (deleted.size === targets.length)
            toast.success(`Deleted ${deleted.size} run${deleted.size === 1 ? "" : "s"}`);
        else toast.error(`Deleted ${deleted.size} / ${targets.length} runs`);
    }

    async function handleBulkRerun(ids: string[]) {
        const taskNames = Array.from(new Set(runsByIds(ids).map((r) => r.task_name)));
        if (taskNames.length === 0) return;
        const results = await Promise.allSettled(taskNames.map((n) => tasksApi.triggerRun(n)));
        const ok = results.filter((r) => r.status === "fulfilled").length;
        if (ok === taskNames.length) toast.success(`Triggered ${ok} task${ok === 1 ? "" : "s"}`);
        else toast.error(`Triggered ${ok} / ${taskNames.length} tasks`);
    }

    let sortedRuns: Run[] = $derived(sortByCreatedAtDesc(runs));

    let selectedRunId = $derived.by(() => {
        if (userSelectedRunId && sortedRuns.some((r) => r.id === userSelectedRunId)) {
            return userSelectedRunId;
        }
        return sortedRuns[0]?.id ?? null;
    });

    let selectedRun = $derived(runs.find((r: Run) => r.id === selectedRunId));
</script>

<PageContainer variant="flush" class="gap-4">
    <!-- Header -->
    <div class="flex shrink-0 items-center justify-between px-1">
        <div>
            <h1 class="text-2xl font-bold tracking-tight text-slate-900">Run History</h1>
            <p class="mt-0.5 text-sm text-slate-500">
                Comprehensive log of all task executions across this instance.
            </p>
        </div>
    </div>

    <!-- Main Content Area -->
    <div class="grid min-h-0 flex-1 grid-cols-1 gap-6 md:grid-cols-12">
        <RunsList
            {runs}
            {selectedRunId}
            onselect={(id) => (userSelectedRunId = id)}
            showFilters
            showTaskName
            headerLabel="Runs"
            emptyText="No runs found"
            bulkActions
            onBulkCancel={handleBulkCancel}
            onBulkDelete={handleBulkDelete}
            onBulkRerun={handleBulkRerun}
        />

        <!-- Right Panel: Run Details -->
        <div
            class="flex flex-col overflow-hidden rounded-xl border border-slate-200 bg-white shadow-sm md:col-span-8 lg:col-span-9"
        >
            <RunDetailPanel
                run={selectedRun}
                {fetchLogs}
                {streamLogs}
                showTaskName
                onDelete={requestDelete}
            />
        </div>
    </div>

    <Modal
        bind:open={deleteConfirmOpen}
        title="Delete Run"
        description="Delete this run? The captured log will also be removed and cannot be recovered."
        size="sm"
    >
        {#snippet footer()}
            <div class="flex justify-end gap-2">
                <Button variant="secondary" size="sm" onclick={() => (deleteConfirmOpen = false)}>
                    Cancel
                </Button>
                <Button variant="danger" size="sm" onclick={confirmDelete} loading={deleting}>
                    {#snippet icon()}<Trash2 size={16} />{/snippet}
                    Delete
                </Button>
            </div>
        {/snippet}
    </Modal>
</PageContainer>
