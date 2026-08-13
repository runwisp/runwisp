<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { Play, Square, RefreshCcw } from "@lucide/svelte";
    import Button from "@runwisp/ui/components/Button.svelte";
    import Modal from "@runwisp/ui/components/Modal.svelte";
    import Alert from "@runwisp/ui/components/Alert.svelte";
    import { SvelteMap } from "svelte/reactivity";
    import { isService, type Task, type Run } from "@runwisp/common";
    import type { LogEvent, LogSlice, RunsListFilters, RunOutputMatch } from "@runwisp/ui";
    import { RunsList, RunDetailPanel } from "@runwisp/ui";
    import { tasksApi } from "$lib/api";
    import { headerSearchStore } from "$lib/stores";
    import { createRunActions } from "$lib/utils/run-actions";
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
        runNotFound = false,
        onSelectRun,
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
        // True when the deep-linked run id (initialRunId) was fetched and doesn't
        // exist under this task — surfaces a "not found" panel instead of quietly
        // falling back to the running/newest run under a dead URL.
        runNotFound?: boolean;
        // Notified whenever the user picks a run (click or freshly triggered),
        // so the route can mirror it into the address bar. The auto-fallback
        // selection (newest/running) is deliberately not reported.
        onSelectRun?: (runId: string | null) => void;
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

    // Output search filters the rail by what each run printed. The search box
    // lives in the app header now; this page owns the async query (it has the
    // API client) and the matched-run map, and feeds the header via the store.
    let outputMatches = $state<Map<string, RunOutputMatch> | null>(null);
    let outputSearchLoading = $state(false);
    let outputSearchSeq = 0;
    // The query most recently handed to the search. While the live header query
    // is ahead of it (mid-type, inside the header's debounce) the search counts
    // as pending even though no request has fired — keeps the rail in its
    // searching state instead of flashing stale results.
    let lastDispatched = $state("");

    async function handleOutputSearch(rawQuery: string) {
        const query = rawQuery.trim();
        lastDispatched = query;
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
            for (const hit of res.items) {
                if (!map.has(hit.runId)) map.set(hit.runId, { line: hit.n, text: hit.text });
            }
            outputMatches = map;
        } catch {
            if (seq === outputSearchSeq) outputMatches = new SvelteMap();
        } finally {
            if (seq === outputSearchSeq) outputSearchLoading = false;
        }
    }

    // The live header query, and the rail's derived search state from it.
    const outputQuery = $derived(headerSearchStore.query);
    const outputSearchActive = $derived(outputQuery.trim().length > 0);
    const outputSearchPending = $derived(
        outputSearchActive &&
            (outputSearchLoading ||
                outputMatches === null ||
                outputQuery.trim() !== lastDispatched),
    );

    // Register the header search whenever the history rail is on screen, and
    // re-register on task change so a query never leaks from one task to the
    // next. The header owns the box + debounce and calls back here.
    $effect(() => {
        if (hideHistory && !historyExpanded) return;
        void task.name;
        headerSearchStore.register({
            placeholder: "Search output across runs…",
            onSearch: (q) => void handleOutputSearch(q),
        });
        return () => headerSearchStore.unregister();
    });

    // Surface the async log-search progress as the header field's spinner.
    $effect(() => {
        headerSearchStore.setLoading(outputSearchLoading);
    });

    let userSelectedRunId = $state<string | null>(null);

    const { handleBulkDelete, handleBulkCancel, handleBulkRerun, deleteSingle } = createRunActions({
        getItems: () => items,
        onOptimisticRemove: (ids) => onOptimisticRemove(ids),
        onOptimisticRestore: (runs) => onOptimisticRestore(runs),
        onRemoved: (ids) => {
            if (userSelectedRunId && ids.has(userSelectedRunId)) userSelectedRunId = null;
        },
    });

    // A run can always be *triggered* — at max concurrency it queues (the modal
    // says so), so concurrency must not gate the button, only its warning.
    // Disabled only when the task forbids API triggering or a trigger is mid-flight.
    const runTriggerable = $derived(!taskIsService && (task.apiTrigger ?? true) && !triggering);

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

    // Report explicit selections upward so the URL can mirror the run on screen.
    // Must stay after the seed effects above: on the first flush they run in
    // declaration order, so userSelectedRunId is already seeded from the deep
    // link when this reports — otherwise the initial null would clobber it.
    $effect(() => {
        onSelectRun?.(userSelectedRunId);
    });

    // The deep-linked run genuinely doesn't exist under this task: its id is the
    // current URL selection, the fetch confirmed it missing, and it isn't in the
    // list. Show a "not found" panel rather than silently falling back to the
    // running/newest run while the URL still points at the dead id.
    let deepLinkMissing = $derived(
        runNotFound &&
            userSelectedRunId !== null &&
            userSelectedRunId === initialRunId &&
            !items.some((r: Run) => r.id === userSelectedRunId),
    );

    let selectedRunId = $derived.by(() => {
        if (userSelectedRunId && items.some((r: Run) => r.id === userSelectedRunId)) {
            return userSelectedRunId;
        }
        if (deepLinkMissing) return null;
        const running = items.find((r: Run) => r.status === "running");
        if (running) return running.id;
        return items[0]?.id ?? null;
    });

    let selectedRun = $derived(items.find((r: Run) => r.id === selectedRunId));

    const envEntries = $derived(
        task.env ? Object.entries(task.env).sort(([a], [b]) => a.localeCompare(b)) : [],
    );
    const showEnvPanel = $derived(envEntries.length > 0 || !!task.envFile || !!task.secretsFile);
</script>

<!-- Card-less, full-bleed: the rail and detail panel fill the content area
     edge-to-edge (cancelling AppLayout's p-6), divided only by the rail's
     right border. The topbar/sidebar are the outer frame; no nested card. -->
<div class="-m-6 flex h-[calc(100%+3rem)] min-h-0 flex-col">
    {#if showEnvPanel}
        <section
            aria-label="Task environment"
            class="shrink-0 border-b border-outline bg-surface-raised px-6 py-3"
        >
            <h2 class="font-mono text-2xs font-medium tracking-[0.16em] text-info uppercase">
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
            {#if task.envFile}
                <p class="mt-2 font-mono text-xs text-on-surface-faint">
                    Includes values from {task.envFile}
                </p>
            {/if}
            {#if task.secretsFile}
                <p class="mt-2 font-mono text-xs text-on-surface-faint">
                    Secrets from {task.secretsFile} (values not exposed)
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
                showFilters
                emptyText="No runs yet"
                bulkActions
                taskNameFilter={task.name}
                onBulkCancel={handleBulkCancel}
                onBulkDelete={handleBulkDelete}
                onBulkRerun={handleBulkRerun}
                getInstanceCount={() => instanceCount}
                outputSearch
                {outputQuery}
                {outputMatches}
                {outputSearchPending}
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
            notFound={deepLinkMissing}
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
    title={serviceStopped ? "Start Service" : "Restart Service"}
    description={serviceStopped
        ? `Start ${task.name}?`
        : instanceCount > 1
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
            serviceStopped ? "Start Now" : "Restart Now",
            "primary",
            serviceStopped ? Play : RefreshCcw,
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
