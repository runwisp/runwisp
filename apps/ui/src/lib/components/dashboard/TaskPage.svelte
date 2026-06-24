<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { Play, Square, RefreshCcw } from "@lucide/svelte";
    import Button from "@runwisp/ui/components/Button.svelte";
    import Modal from "@runwisp/ui/components/Modal.svelte";
    import Alert from "@runwisp/ui/components/Alert.svelte";
    import { SvelteMap } from "svelte/reactivity";
    import { isService, type Task, type Run, type RunSelector } from "@runwisp/common";
    import type { LogEvent, LogSlice, RunsListFilters, RunOutputMatch } from "@runwisp/ui";
    import { RunsList, RunDetailPanel, toast, extractErrorMessage } from "@runwisp/ui";
    import { runsApi, tasksApi } from "$lib/api";
    import ParamForm from "./ParamForm.svelte";

    let {
        task,
        cloudMode = false,
        items,
        total,
        loading = false,
        filters = $bindable(),
        onLoadMore,
        onOptimisticRemove,
        onOptimisticRestore,
        concurrencyReached = false,
        triggering = false,
        restarting = false,
        serviceStopped = false,
        stoppingService = false,
        onRun,
        onStop,
        onRestart,
        onStopService,
        fetchLogs,
        streamLogs,
        fetchLineHistory,
        initialRunId = null,
        initialHighlightLine = null,
        selectRunId = null,
    } = $props<{
        task: Task;
        cloudMode?: boolean;
        items: Run[];
        total: number;
        loading?: boolean;
        filters: RunsListFilters;
        onLoadMore: () => void;
        onOptimisticRemove: (ids: string[]) => void;
        onOptimisticRestore: (runs: Run[]) => void;
        concurrencyReached?: boolean;
        triggering?: boolean;
        restarting?: boolean;
        serviceStopped?: boolean;
        stoppingService?: boolean;
        onRun: (params?: Record<string, string | null>) => void;
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
        fetchLineHistory?: (runId: string, lineNum: number) => Promise<string[][]>;
        initialRunId?: string | null;
        initialHighlightLine?: number | null;
        selectRunId?: string | null;
    }>();

    const taskIsService = $derived(isService(task.kind));
    const instanceCount = $derived(taskIsService ? Math.max(1, task.instances ?? 1) : 0);
    const hideHistory = $derived(taskIsService && instanceCount == 1);
    let historyExpanded = $state(false);
    let confirmOpen = $state(false);
    let runParamValues = $state<Record<string, string | null>>({});
    let runParamsValid = $state(true);
    const taskParams = $derived(task.parameters ?? []);
    const hasParams = $derived(taskParams.length > 0);
    // The Run modal only needs a body when there's something to show — the
    // concurrency warning or the parameter form. Passing `children`
    // conditionally keeps Modal from rendering an empty padded band otherwise.
    const showRunBody = $derived(concurrencyReached || (hasParams && confirmOpen));

    // Seed values for the Run modal's parameter form: null = start from the
    // task defaults ("Run"); a prior run's params = pre-fill from it ("Run
    // again"). `runFormSeq` keys the form so each open (or a reset) re-mounts
    // it and re-seeds — ParamForm captures its values once at construction.
    let runSeed = $state<Record<string, string | null> | null>(null);
    let runFormSeq = $state(0);

    function openRun() {
        runSeed = null;
        runFormSeq++;
        confirmOpen = true;
    }

    function openRunAgain() {
        runSeed = selectedRun?.params ?? null;
        runFormSeq++;
        confirmOpen = true;
    }

    function resetRunToDefaults() {
        runSeed = null;
        runFormSeq++;
    }
    let stopConfirmOpen = $state(false);
    let restartConfirmOpen = $state(false);
    let stopServiceConfirmOpen = $state(false);

    // Output search lives in the history rail (the artifact's ".history__search"):
    // it filters runs by what they printed. The rail owns the box + debounce; this
    // page owns the async query (it has the API client) and the matched-run map.
    let outputSearchOpen = $state(false);
    let outputMatches = $state<Map<string, RunOutputMatch> | null>(null);
    let outputSearchLoading = $state(false);
    let outputSearchSeq = 0;

    async function handleOutputSearch(query: string) {
        if (!query) {
            outputSearchSeq++;
            outputMatches = null;
            outputSearchLoading = false;
            return;
        }
        const seq = ++outputSearchSeq;
        outputSearchLoading = true;
        // Drop stale hits so the rail shows its searching state, not the
        // previous query's results, while this one is in flight.
        outputMatches = null;
        try {
            const res = await tasksApi.searchLogs(task.name, {
                q: query,
                regex: false,
                case: false,
                limit: 200,
            });
            if (seq !== outputSearchSeq) return; // a newer query superseded this one
            const map = new SvelteMap<string, RunOutputMatch>();
            for (const hit of res.hits) {
                if (!map.has(hit.run_id)) map.set(hit.run_id, { line: hit.n, text: hit.text });
            }
            outputMatches = map;
        } catch {
            if (seq === outputSearchSeq) outputMatches = new SvelteMap();
        } finally {
            if (seq === outputSearchSeq) outputSearchLoading = false;
        }
    }

    // ⌘K / Ctrl+K opens the rail's output-search box (focus follows via autofocus).
    function onWindowKeydown(e: KeyboardEvent) {
        if (e.key.toLowerCase() !== "k" || !(e.metaKey || e.ctrlKey) || e.shiftKey || e.altKey) {
            return;
        }
        const target = e.target;
        if (
            target instanceof HTMLInputElement ||
            target instanceof HTMLTextAreaElement ||
            (target instanceof HTMLElement && target.isContentEditable)
        ) {
            return;
        }
        e.preventDefault();
        outputSearchOpen = true;
    }

    const UNDO_MS = 5000;

    async function handleBulkDelete(selector: RunSelector, affected: Run[]) {
        if (affected.length === 0) return;
        const removedIds = new Set(affected.map((r) => r.id));
        const snapshot = items.filter((r: Run) => removedIds.has(r.id));
        onOptimisticRemove([...removedIds]);
        if (userSelectedRunId && removedIds.has(userSelectedRunId)) userSelectedRunId = null;

        try {
            const count = await runsApi.bulkDelete(selector);
            const restoreSelector: RunSelector = {
                match_all: false,
                ids: [...removedIds],
            };
            toast.success(count === 1 ? "Run deleted" : `${count} runs deleted`, {
                duration: UNDO_MS,
                action: {
                    label: "Undo",
                    onClick: () => void undoDelete(restoreSelector, snapshot),
                },
            });
        } catch (err) {
            onOptimisticRestore(snapshot);
            toast.error(extractErrorMessage(err, "Failed to delete runs"));
        }
    }

    async function undoDelete(selector: RunSelector, snapshot: Run[]) {
        try {
            await runsApi.bulkRestore(selector);
            onOptimisticRestore(snapshot);
        } catch (err) {
            toast.error(extractErrorMessage(err, "Failed to restore runs"));
        }
    }

    async function handleBulkCancel(selector: RunSelector, affected: Run[]) {
        if (affected.length === 0) return;
        try {
            const count = await runsApi.bulkCancel(selector);
            toast.success(count === 1 ? "Cancelled 1 run" : `Cancelled ${count} runs`);
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
            toast.success(`Triggered ${triggered.length} ${label}`, {
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

    async function undoRerun(triggered: { task_name: string; run_id: string }[]) {
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
        const target = items.find((r: Run) => r.id === runId);
        if (!target) return;
        void handleBulkDelete({ match_all: false, ids: [runId] }, [target]);
    }

    // A run can always be *triggered* — at max concurrency it queues (the modal
    // says so), so concurrency must not gate the button, only its warning.
    // Disabled only when the task forbids API triggering or a trigger is mid-flight.
    const runTriggerable = $derived(!taskIsService && (task.api_trigger ?? true) && !triggering);

    // In cloud mode the cloud owns scheduling/dispatch; triggering here is the
    // operator's "run it here, now" escape hatch against the local runner.
    // Frame the confirm honestly rather than implying it's the canonical trigger.
    const runConfirmLabel = $derived(cloudMode ? "Run Here" : "Run Now");
    const runModalTitle = $derived(cloudMode ? "Run on this runner" : "Run Task");
    const runModalDescription = $derived(
        cloudMode
            ? `Run ${task.name} on this runner now? This triggers an immediate local run; scheduling stays with the cloud.`
            : `Trigger a new run of ${task.name}?`,
    );

    let userSelectedRunId = $state<string | null>(null);
    let highlightLine = $state<number | null>(null);

    $effect(() => {
        if (initialHighlightLine !== null && initialHighlightLine !== undefined) {
            highlightLine = initialHighlightLine;
        }
    });

    $effect(() => {
        if (initialRunId) userSelectedRunId = initialRunId;
    });

    $effect(() => {
        if (selectRunId) userSelectedRunId = selectRunId;
    });

    let selectedRunId = $derived.by(() => {
        if (userSelectedRunId && items.some((r: Run) => r.id === userSelectedRunId)) {
            return userSelectedRunId;
        }
        const running = items.find((r: Run) => r.status === "running");
        if (running) return running.id;
        return items[0]?.id ?? null;
    });

    let selectedRun = $derived(items.find((r: Run) => r.id === selectedRunId));

    const envEntries = $derived(
        task.env ? Object.entries(task.env).sort(([a], [b]) => a.localeCompare(b)) : [],
    );
    const showEnvPanel = $derived(envEntries.length > 0 || !!task.env_file || !!task.secrets_file);
</script>

<svelte:window onkeydown={onWindowKeydown} />

<!-- Card-less, full-bleed: the rail and detail panel fill the content area
     edge-to-edge (cancelling AppLayout's p-6), divided only by the rail's
     right border. The topbar/sidebar are the outer frame; no nested card. -->
<div class="-m-6 flex h-[calc(100%+3rem)] min-h-0 flex-col">
    {#if showEnvPanel}
        <section
            aria-label="Task environment"
            class="shrink-0 border-b border-outline bg-surface-raised px-6 py-3"
        >
            <h2 class="text-xs font-semibold tracking-wide text-on-surface-muted uppercase">
                Environment
            </h2>
            {#if envEntries.length > 0}
                <dl class="mt-2 grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 font-mono text-xs">
                    {#each envEntries as [key, value] (key)}
                        <dt class="text-on-surface">{key}</dt>
                        <dd class="break-all text-on-surface-muted">{value}</dd>
                    {/each}
                </dl>
            {/if}
            {#if task.env_file}
                <p class="mt-2 font-mono text-xs text-on-surface-faint">
                    Includes values from {task.env_file}
                </p>
            {/if}
            {#if task.secrets_file}
                <p class="mt-2 font-mono text-xs text-on-surface-faint">
                    Secrets from {task.secrets_file} (values not exposed)
                </p>
            {/if}
        </section>
    {/if}

    <div class="flex min-h-0 flex-1 flex-col md:flex-row">
        {#if !hideHistory || historyExpanded}
            <RunsList
                flush
                {items}
                {total}
                {loading}
                bind:filters
                {onLoadMore}
                {selectedRunId}
                onselect={(id) => (userSelectedRunId = id)}
                emptyText="No runs yet"
                bulkActions
                taskNameFilter={task.name}
                onBulkCancel={handleBulkCancel}
                onBulkDelete={handleBulkDelete}
                onBulkRerun={handleBulkRerun}
                getInstanceCount={() => instanceCount}
                outputSearch
                bind:outputSearchOpen
                onOutputSearch={handleOutputSearch}
                {outputMatches}
                {outputSearchLoading}
            />
        {/if}

        <RunDetailPanel
            run={selectedRun}
            {fetchLogs}
            {streamLogs}
            {fetchLineHistory}
            onDelete={deleteSingle}
            onRun={runTriggerable ? openRun : undefined}
            onRunAgain={runTriggerable && hasParams ? openRunAgain : undefined}
            onRunTask={runTriggerable ? openRun : undefined}
            onStop={!taskIsService && onStop ? () => (stopConfirmOpen = true) : undefined}
            onStopService={taskIsService && onStopService
                ? () => (stopServiceConfirmOpen = true)
                : undefined}
            onRestartService={taskIsService && onRestart
                ? () => (restartConfirmOpen = true)
                : undefined}
            {serviceStopped}
            serviceBusy={stoppingService || restarting}
            onToggleHistory={hideHistory ? () => (historyExpanded = !historyExpanded) : undefined}
            historyVisible={historyExpanded}
            {highlightLine}
            getInstanceCount={() => instanceCount}
        />
    </div>
</div>

<Modal
    bind:open={confirmOpen}
    title={runModalTitle}
    description={runModalDescription}
    size={hasParams ? "md" : "sm"}
    children={showRunBody ? runModalBody : undefined}
>
    {#snippet footer()}
        <div class="flex justify-end gap-2">
            <Button variant="secondary" size="sm" onclick={() => (confirmOpen = false)}>
                Cancel
            </Button>
            <Button
                variant="primary"
                size="sm"
                disabled={hasParams && !runParamsValid}
                onclick={() => {
                    confirmOpen = false;
                    onRun(hasParams ? runParamValues : undefined);
                }}
            >
                {#snippet icon()}<Play size={16} />{/snippet}
                {runConfirmLabel}
            </Button>
        </div>
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

{#snippet runModalBody()}
    {#if concurrencyReached}
        {@render concurrencyWarning()}
    {/if}
    {#if hasParams && confirmOpen}
        {#if runSeed}
            <div class="mb-3 flex items-center justify-between gap-2">
                <span class="text-xs text-on-surface-muted">Pre-filled from the selected run.</span>
                <button
                    type="button"
                    class="text-xs font-medium text-primary hover:underline"
                    onclick={resetRunToDefaults}
                >
                    Reset to defaults
                </button>
            </div>
        {/if}
        {#key runFormSeq}
            <ParamForm
                params={taskParams}
                initial={runSeed}
                bind:value={runParamValues}
                bind:valid={runParamsValid}
            />
        {/key}
    {/if}
{/snippet}

{#snippet concurrencyWarning()}
    <Alert variant="warning">
        This task is already running at its maximum concurrency. Your run will be <strong
            >queued</strong
        > and will start automatically once a slot becomes available.
    </Alert>
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
