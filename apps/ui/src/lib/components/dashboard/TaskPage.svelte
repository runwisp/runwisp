<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { Play, PanelLeftClose, History, Square, RefreshCcw, Search } from "@lucide/svelte";
    import Button from "@runwisp/ui/components/Button.svelte";
    import Modal from "@runwisp/ui/components/Modal.svelte";
    import Alert from "@runwisp/ui/components/Alert.svelte";
    import Card from "@runwisp/ui/components/Card.svelte";
    import { isService, type Task, type Run, type RunSelector } from "@runwisp/common";
    import type { LogEvent, LogSlice, RunsListFilters } from "@runwisp/ui";
    import PageContainer from "@runwisp/ui/components/PageContainer.svelte";
    import PageHeader from "@runwisp/ui/components/PageHeader.svelte";
    import { RunsList, RunDetailPanel, toast, extractErrorMessage } from "@runwisp/ui";
    import { runsApi } from "$lib/api";
    import LogSearchPanel from "./LogSearchPanel.svelte";
    import type { LogSearchHit } from "$lib/logs";

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
        initialHighlightLine?: number | null;
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
    let searchOpen = $state(false);

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
        searchOpen = true;
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

    const runLaunchable = $derived(
        !taskIsService && (task.api_trigger ?? true) && !triggering && !concurrencyReached,
    );
    const runDisabledReason = $derived.by(() => {
        if (taskIsService) return "Services are managed by the supervisor";
        if (!(task.api_trigger ?? true)) return "API triggering is disabled for this task";
        if (concurrencyReached) return "Max concurrency reached";
        return "";
    });

    // In cloud mode the cloud owns scheduling/dispatch; this button is the
    // operator's "run it here, now" escape hatch against the local runner.
    // Frame it honestly rather than implying it's the canonical trigger.
    const runButtonLabel = $derived(cloudMode ? "Run Here" : "Run Task");
    const runConfirmLabel = $derived(cloudMode ? "Run Here" : "Run Now");
    const runActionTitle = $derived(
        runDisabledReason || (cloudMode ? "Run on this runner now" : ""),
    );
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

    function handleSearchHit(hit: LogSearchHit) {
        userSelectedRunId = hit.run_id;
        // Re-trigger by setting a new array+number — LogConsole's effect runs
        // on every prop assignment, so the same line number twice is fine.
        highlightLine = hit.n;
    }

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

<LogSearchPanel bind:open={searchOpen} taskName={task.name} onSelectHit={handleSearchHit} />

<PageContainer variant="flush" class="gap-4">
    <PageHeader title={task.name} subtitle={task.description || "No description provided."}>
        {#snippet actions()}
            <Button
                variant="ghost"
                size="sm"
                onclick={() => (searchOpen = true)}
                title="Search logs (⌘K)"
            >
                {#snippet icon()}<Search size={16} />{/snippet}
                Search
            </Button>
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
                    title={runActionTitle}
                >
                    {#snippet icon()}<Play size={16} />{/snippet}
                    {runButtonLabel}
                </Button>
            {/if}
        {/snippet}
    </PageHeader>

    {#if showEnvPanel}
        <Card padding="none" class="shrink-0">
            <section aria-label="Task environment" class="px-4 py-3">
                <h2 class="text-xs font-semibold tracking-wide text-on-surface-muted uppercase">
                    Environment
                </h2>
                {#if envEntries.length > 0}
                    <dl
                        class="mt-2 grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 font-mono text-xs"
                    >
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
        </Card>
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
            />
        {/if}

        <!-- Right Panel: Run Details -->
        <Card
            padding="none"
            class="flex flex-col {hideHistory && !historyExpanded
                ? ''
                : 'md:col-span-8 lg:col-span-9'}"
            bodyClass="flex min-h-0 flex-1 flex-col"
        >
            <RunDetailPanel
                run={selectedRun}
                {fetchLogs}
                {streamLogs}
                onDelete={deleteSingle}
                {highlightLine}
            />
        </Card>
    </div>

    <Modal
        bind:open={confirmOpen}
        title={runModalTitle}
        description={runModalDescription}
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
                runConfirmLabel,
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
