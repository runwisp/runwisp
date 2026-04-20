<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { Play, PanelLeftClose, History, Square } from "@lucide/svelte";
    import Button from "@runwisp/ui/components/Button.svelte";
    import Modal from "@runwisp/ui/components/Modal.svelte";
    import { isLongRunningTask, type Task, type Run } from "@runwisp/common";
    import type { LogEvent, LogSlice } from "@runwisp/ui";
    import PageContainer from "@runwisp/ui/components/PageContainer.svelte";
    import { RunsList, RunDetailPanel } from "@runwisp/ui";
    import { sortByCreatedAtDesc } from "$lib/utils/sort";

    let {
        task,
        runs = [],
        concurrencyReached = false,
        triggering = false,
        stopping = false,
        onRun,
        onStop,
        fetchLogs,
        streamLogs,
        initialRunId = null,
        selectRunId = null,
    } = $props<{
        task: Task;
        runs?: Run[];
        concurrencyReached?: boolean;
        triggering?: boolean;
        stopping?: boolean;
        onRun: () => void;
        onStop?: (runId: string) => void;
        fetchLogs: (
            runId: string,
            from: number,
            to: number,
        ) => Promise<LogSlice | LogEvent | void> | LogSlice | LogEvent | void;
        streamLogs?: (runId: string, onEvent: (event: LogEvent) => void) => () => void;
        initialRunId?: string | null;
        selectRunId?: string | null;
    }>();

    const isLongRunning = $derived(
        isLongRunningTask(task.execution?.restart, task.execution?.concurrency?.limit),
    );
    let historyExpanded = $state(false);
    let confirmOpen = $state(false);
    let stopConfirmOpen = $state(false);

    let sortedRuns: Run[] = $derived(sortByCreatedAtDesc(runs));

    let userSelectedRunId = $state<string | null>(null);

    $effect(() => {
        if (initialRunId) userSelectedRunId = initialRunId;
    });

    $effect(() => {
        if (selectRunId) userSelectedRunId = selectRunId;
    });

    let selectedRunId = $derived.by(() => {
        if (userSelectedRunId && runs.some((r: Run) => r.id === userSelectedRunId)) {
            return userSelectedRunId;
        }
        const running = runs.find((r: Run) => r.status === "running");
        if (running) return running.id;
        return sortedRuns[0]?.id ?? null;
    });

    let selectedRun = $derived(runs.find((r: Run) => r.id === selectedRunId));
</script>

<PageContainer variant="flush" class="gap-4">
    <!-- Header -->
    <div class="flex shrink-0 items-center justify-between px-1">
        <div>
            <h1 class="text-2xl font-bold tracking-tight text-slate-900">{task.name}</h1>
            <p class="mt-0.5 text-sm text-slate-500">
                {task.description || "No description provided."}
            </p>
        </div>
        <div class="flex items-center gap-2">
            {#if isLongRunning}
                <Button
                    variant="ghost"
                    size="sm"
                    onclick={() => (historyExpanded = !historyExpanded)}
                    title={historyExpanded ? "Hide history" : "Show history"}
                >
                    {#snippet icon()}
                        {#if historyExpanded}
                            <PanelLeftClose size={16} />
                        {:else}
                            <History size={16} />
                        {/if}
                    {/snippet}
                    {historyExpanded ? "Hide History" : "History"}
                </Button>
            {/if}
            {#if selectedRun?.status === "running" && onStop}
                <Button
                    variant="danger"
                    onclick={() => (stopConfirmOpen = true)}
                    loading={stopping}
                >
                    {#snippet icon()}<Square size={16} />{/snippet}
                    Stop
                </Button>
            {/if}
            <Button variant="primary" onclick={() => (confirmOpen = true)} loading={triggering}>
                {#snippet icon()}<Play size={16} />{/snippet}
                Run Task
            </Button>
        </div>
    </div>

    <!-- Main Content Area -->
    <div
        class={[
            "grid min-h-0 flex-1 gap-6",
            isLongRunning && !historyExpanded ? "grid-cols-1" : "grid-cols-1 md:grid-cols-12",
        ]}
    >
        {#if !isLongRunning || historyExpanded}
            <RunsList
                {runs}
                {selectedRunId}
                onselect={(id) => (userSelectedRunId = id)}
                emptyText="No runs yet"
            />
        {/if}

        <!-- Right Panel: Run Details -->
        <div
            class={[
                "flex flex-col overflow-hidden rounded-xl border border-slate-200 bg-white shadow-sm",
                isLongRunning && !historyExpanded ? "" : "md:col-span-8 lg:col-span-9",
            ]}
        >
            <RunDetailPanel run={selectedRun} {fetchLogs} {streamLogs} />
        </div>
    </div>

    <Modal
        bind:open={confirmOpen}
        title="Run Task"
        description="Trigger a new run of {task.name}?"
        size="sm"
    >
        {#if concurrencyReached}
            {@render concurrencyWarning()}
        {/if}
        {#snippet footer()}
            {@render confirmFooter(
                () => (confirmOpen = false),
                () => {
                    confirmOpen = false;
                    onRun();
                },
                "Run Now",
                "primary",
                Play,
            )}
        {/snippet}
    </Modal>

    <Modal
        bind:open={stopConfirmOpen}
        title="Stop Run"
        description="Stop the current run of {task.name}?"
        size="sm"
    >
        {#snippet footer()}
            {@render confirmFooter(
                () => (stopConfirmOpen = false),
                () => {
                    stopConfirmOpen = false;
                    if (onStop && selectedRun) onStop(selectedRun.id);
                },
                "Stop Now",
                "danger",
                Square,
            )}
        {/snippet}
    </Modal>
</PageContainer>

{#snippet concurrencyWarning()}
    <div
        class="flex items-start gap-3 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800"
    >
        <span class="mt-0.5 shrink-0 text-amber-500">⚠</span>
        <span>
            This task is already running at its maximum concurrency. Your run will be <strong
                >queued</strong
            > and will start automatically once a slot becomes available.
        </span>
    </div>
{/snippet}

{#snippet confirmFooter(
    cancel: () => void,
    confirm: () => void,
    label: string,
    variant: "primary" | "danger",
    Icon: typeof Play,
)}
    <div class="flex justify-end gap-2">
        <Button variant="secondary" size="sm" onclick={cancel}>Cancel</Button>
        <Button {variant} size="sm" onclick={confirm}>
            {#snippet icon()}<Icon size={16} />{/snippet}
            {label}
        </Button>
    </div>
{/snippet}
