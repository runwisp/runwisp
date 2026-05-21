<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { Play, PanelLeftClose, History, Square, RefreshCcw, Trash2 } from "@lucide/svelte";
    import { SvelteSet } from "svelte/reactivity";
    import Button from "@runwisp/ui/components/Button.svelte";
    import Modal from "@runwisp/ui/components/Modal.svelte";
    import { isService, type Task, type Run } from "@runwisp/common";
    import type { LogEvent, LogSlice } from "@runwisp/ui";
    import PageContainer from "@runwisp/ui/components/PageContainer.svelte";
    import { RunsList, RunDetailPanel, toast, extractErrorMessage } from "@runwisp/ui";
    import { sortByCreatedAtDesc } from "$lib/utils/sort";
    import { tasksApi } from "$lib/api";

    let {
        task,
        runs = $bindable([]),
        concurrencyReached = false,
        triggering = false,
        stopping = false,
        restarting = false,
        serviceStopped = false,
        stoppingService = false,
        onRun,
        onStop,
        onRestart,
        onStopService,
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
        restarting?: boolean;
        serviceStopped?: boolean;
        stoppingService?: boolean;
        onRun: () => void;
        onStop?: (runId: string) => void;
        onRestart?: () => void;
        onStopService?: () => void;
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
        initialRunId?: string | null;
        selectRunId?: string | null;
    }>();

    const taskIsService = $derived(isService(task.kind));
    const instanceCount = $derived(taskIsService ? Math.max(1, task.instances ?? 1) : 0);
    const hideHistory = $derived(taskIsService && instanceCount == 1);
    let historyExpanded = $state(false);
    let confirmOpen = $state(false);
    let stopConfirmOpen = $state(false);
    let restartConfirmOpen = $state(false);
    let stopServiceConfirmOpen = $state(false);
    let deleteConfirmOpen = $state(false);
    let deleteTargetId = $state<string | null>(null);
    let deleting = $state(false);

    function requestDelete(runId: string) {
        deleteTargetId = runId;
        deleteConfirmOpen = true;
    }

    async function confirmDelete() {
        const id = deleteTargetId;
        if (!id) return;
        deleting = true;
        try {
            await tasksApi.deleteRun(task.name, id);
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

    const runLaunchable = $derived(
        !taskIsService && (task.api_trigger ?? true) && !triggering && !concurrencyReached,
    );
    const runDisabledReason = $derived.by(() => {
        if (taskIsService) return "Services are managed by the supervisor";
        if (!(task.api_trigger ?? true)) return "API triggering is disabled for this task";
        if (concurrencyReached) return "Max concurrency reached";
        return "";
    });

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

    const envEntries = $derived(
        task.env ? Object.entries(task.env).sort(([a], [b]) => a.localeCompare(b)) : [],
    );
    const showEnvPanel = $derived(envEntries.length > 0 || !!task.env_file);
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
            {#if hideHistory}
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
            {#if !taskIsService && selectedRun?.status === "running" && onStop}
                <Button
                    variant="danger"
                    onclick={() => (stopConfirmOpen = true)}
                    loading={stopping}
                >
                    {#snippet icon()}<Square size={16} />{/snippet}
                    Stop
                </Button>
            {/if}
            {#if taskIsService}
                {#if serviceStopped}
                    <Button
                        variant="primary"
                        onclick={() => (restartConfirmOpen = true)}
                        loading={restarting}
                        disabled={!onRestart}
                    >
                        {#snippet icon()}<RefreshCcw size={16} />{/snippet}
                        Restart Service
                    </Button>
                {:else}
                    <Button
                        variant="danger"
                        onclick={() => (stopServiceConfirmOpen = true)}
                        loading={stoppingService}
                        disabled={!onStopService}
                        title="Stops the service for the rest of the daemon's lifetime"
                    >
                        {#snippet icon()}<Square size={16} />{/snippet}
                        Stop Service
                    </Button>
                {/if}
            {:else}
                <Button
                    variant="primary"
                    onclick={() => (confirmOpen = true)}
                    loading={triggering}
                    disabled={!runLaunchable}
                    title={runDisabledReason}
                >
                    {#snippet icon()}<Play size={16} />{/snippet}
                    Run Task
                </Button>
            {/if}
        </div>
    </div>

    {#if showEnvPanel}
        <section
            class="shrink-0 rounded-xl border border-slate-200 bg-white px-4 py-3 shadow-sm"
            aria-label="Task environment"
        >
            <h2 class="text-xs font-semibold tracking-wide text-slate-500 uppercase">
                Environment
            </h2>
            {#if envEntries.length > 0}
                <dl class="mt-2 grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 font-mono text-xs">
                    {#each envEntries as [key, value] (key)}
                        <dt class="text-slate-700">{key}</dt>
                        <dd class="break-all text-slate-500">{value}</dd>
                    {/each}
                </dl>
            {/if}
            {#if task.env_file}
                <p class="mt-2 font-mono text-xs text-slate-400">
                    Loaded from {task.env_file} (values not exposed)
                </p>
            {/if}
        </section>
    {/if}

    <!-- Main Content Area -->
    <div
        class={[
            "grid min-h-0 flex-1 gap-6",
            hideHistory && !historyExpanded ? "grid-cols-1" : "grid-cols-1 md:grid-cols-12",
        ]}
    >
        {#if !hideHistory || historyExpanded}
            <RunsList
                {runs}
                {selectedRunId}
                onselect={(id) => (userSelectedRunId = id)}
                emptyText="No runs yet"
                bulkActions
                onBulkCancel={handleBulkCancel}
                onBulkDelete={handleBulkDelete}
                onBulkRerun={handleBulkRerun}
            />
        {/if}

        <!-- Right Panel: Run Details -->
        <div
            class={[
                "flex flex-col overflow-hidden rounded-xl border border-slate-200 bg-white shadow-sm",
                hideHistory && !historyExpanded ? "" : "md:col-span-8 lg:col-span-9",
            ]}
        >
            <RunDetailPanel run={selectedRun} {fetchLogs} {streamLogs} onDelete={requestDelete} />
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

    <Modal
        bind:open={restartConfirmOpen}
        title="Restart Service"
        description={instanceCount > 1
            ? `Cancel and restart all ${instanceCount} instances of ${task.name}?`
            : `Cancel and restart ${task.name}?`}
        size="sm"
    >
        {#snippet footer()}
            {@render confirmFooter(
                () => (restartConfirmOpen = false),
                () => {
                    restartConfirmOpen = false;
                    onRestart?.();
                },
                "Restart Now",
                "primary",
                RefreshCcw,
            )}
        {/snippet}
    </Modal>

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

    <Modal
        bind:open={stopServiceConfirmOpen}
        title="Stop Service"
        description={`Stop ${task.name}? The daemon will not restart it until you click Restart or the daemon itself restarts.`}
        size="sm"
    >
        {#snippet footer()}
            {@render confirmFooter(
                () => (stopServiceConfirmOpen = false),
                () => {
                    stopServiceConfirmOpen = false;
                    onStopService?.();
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
