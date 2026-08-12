<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { untrack } from "svelte";
    import {
        Hash,
        Check,
        Terminal as TerminalIcon,
        Download,
        Trash2,
        Play,
        Square,
        MousePointerClick,
        RotateCw,
        RefreshCcw,
        ChevronDown,
        RotateCcwClock,
        PanelLeftClose,
        SlidersHorizontal,
        Maximize2,
        Minimize2,
        TextWrap,
        SearchX,
    } from "@lucide/svelte";
    import Button from "../Button.svelte";
    import EmptyState from "../EmptyState.svelte";
    import LogConsole from "../LogConsole.svelte";
    import Popover from "../Popover.svelte";
    import Tooltip from "../Tooltip.svelte";
    import { portal } from "../../actions/portal.js";
    import type { Run } from "./types.js";
    import { isLogEvent, type LogEvent, type LogSlice } from "../../log-console/types.js";
    import { formatClockTime, formatCalendarDate } from "../../utils/format.js";
    import { formatShortId } from "../../utils/id.js";
    import { TickingNow } from "../../utils/ticking-now.svelte.js";
    import { getRunStatusConfig, runDisplayStatus } from "./status-config.js";
    import {
        runDuration,
        runStartDelay,
        runVerdict,
        formatTriggeredByLabel,
        runRetryLabel,
        instanceSuffix,
    } from "./run-helpers.js";

    let {
        run,
        fetchLogs,
        streamLogs,
        fetchLineHistory,
        showTaskName = false,
        onDelete,
        onRun,
        onRunAgain,
        onStop,
        onRunTask,
        onStopService,
        onRestartService,
        serviceStopped = false,
        serviceBusy = false,
        onToggleHistory,
        historyVisible = false,
        highlightLine = null,
        getInstanceCount = () => 1,
        notFound = false,
    }: {
        run: Run | undefined;
        fetchLogs: (
            runId: string,
            from: number,
            to: number,
        ) => Promise<LogSlice | LogEvent | undefined> | LogSlice | LogEvent | undefined;
        streamLogs?: (
            runId: string,
            onEvent: (event: LogEvent) => void,
            initialState?: { fromLine: number },
        ) => () => void;
        // Resolves the prior whole-region frames of a settled progress bar /
        // redraw line so the log console can offer inline rewind. Optional.
        fetchLineHistory?: (runId: string, lineNum: number) => Promise<string[][]>;
        showTaskName?: boolean;
        onDelete?: (runId: string) => void;
        // Start a new run with default options. When set, the action cluster's
        // primary control is a Run button (the cold-start / re-trigger path).
        onRun?: (() => void) | undefined;
        // Re-run reusing the selected run's parameters. Passed only when the task
        // actually has parameters (otherwise it is identical to onRun); it becomes
        // the dropdown half of the Run split button. Omitted on the cross-task
        // runs page or for tasks whose API trigger is disabled.
        onRunAgain?: (() => void) | undefined;
        // Stop the selected run while it is live. When provided and the run is
        // running, the action cluster shows Stop alongside Run.
        onStop?: ((runId: string) => void) | undefined;
        // Trigger the task from the empty state (no run selected yet) — the
        // cold-start path so a never-run task is still launchable from here.
        onRunTask?: (() => void) | undefined;
        // Service lifecycle controls. When either is set the cluster swaps the
        // Run control for Stop Service / Restart Service (chosen by serviceStopped).
        onStopService?: (() => void) | undefined;
        onRestartService?: (() => void) | undefined;
        serviceStopped?: boolean;
        serviceBusy?: boolean;
        // Toggle the (single-instance service) history rail from the panel header,
        // since these tasks hide the rail by default and there is no top bar.
        onToggleHistory?: (() => void) | undefined;
        historyVisible?: boolean;
        highlightLine?: number | null;
        // Resolves a task's currently configured instance count so multi-instance
        // services render a 1-based #N suffix. Defaults to single-instance.
        getInstanceCount?: (taskName: string) => number;
        // True when a deep-linked run id resolved to no run (deleted by retention,
        // or never existed). The empty state then says so plainly instead of the
        // generic "Select a run" — the caller must not silently substitute another.
        notFound?: boolean;
    } = $props();

    let canDelete = $derived.by(() => {
        if (!run || !onDelete) return false;
        const status = runDisplayStatus(run);
        return status !== "running" && status !== "pending";
    });

    // Optimistic per-second clock so a live run's "Ran for" duration counts up
    // between SSE events. The interval only runs while the run is in-flight; it
    // is torn down as soon as the run ends (or the panel is destroyed).
    const durationTicker = new TickingNow(1000);
    $effect(() => {
        if (run?.status !== "running") return;
        return durationTicker.start();
    });

    const TAIL_LINES = 1000;

    let logConsole = $state<{ onStream: (event: LogEvent) => void } | null>(null);

    // Derive a stable scalar so the effect only re-runs when the id actually
    // changes, not on every run object reference swap from SSE array updates.
    let runId = $derived(run?.id);

    $effect(() => {
        const id = runId;
        if (!id) return;

        const stream = streamLogs;
        if (!stream) return;

        let cleanup: (() => void) | undefined;
        let cancelled = false;

        untrack(() => {
            void (async () => {
                // Paint the tail in one batched page so the console opens
                // already at the newest output, instead of replaying 1000
                // backfill lines over SSE and visibly scrolling down to reach
                // the end. The live stream then picks up after the seeded tail
                // (its disk backfill from that anchor closes the fetch↔stream
                // race, and the daemon dedupes anything already shown).
                let fromLine = -TAIL_LINES;
                try {
                    const seed = await fetchLogs(id, -TAIL_LINES, -1);
                    if (cancelled) return;
                    if (isLogEvent(seed)) {
                        if (logConsole) logConsole.onStream(seed);
                        if (seed.finished) return; // ended run: nothing live to follow
                        if (seed.sizeLines > 0) fromLine = seed.sizeLines;
                    }
                } catch {
                    // Seed failed — fall back to the SSE tail backfill.
                }
                if (cancelled) return;
                cleanup = stream(
                    id,
                    (event: LogEvent) => {
                        if (logConsole) logConsole.onStream(event);
                    },
                    { fromLine },
                );
            })();
        });

        return () => {
            cancelled = true;
            if (cleanup) cleanup();
        };
    });

    // Maximize the console to a full-bleed overlay. The console wrapper is
    // never remounted — `maximizePortal` relocates the *live* node to <body>
    // (escaping any transformed/blurred ancestor that would otherwise trap a
    // position:fixed child) and moves it back on restore, so the SSE stream,
    // scroll position, and the {#key run.id} mount all survive the toggle.
    let consoleMaximized = $state(false);
    // Wrap long console lines instead of horizontally scrolling them. Owned
    // here so the toolbar toggle and the <LogConsole> share one source of truth.
    let consoleWrap = $state(false);

    // A run going away (deselect) tears down the console; drop the overlay too.
    $effect(() => {
        if (!run) consoleMaximized = false;
    });

    // Inline-confirm popover for delete, and a transient "Copied" on the run-id chip.
    let confirmDeleteOpen = $state(false);
    // Dropdown half of the Run split button (the "reuse parameters" variant).
    let runMenuOpen = $state(false);
    let copiedId = $state(false);
    let copyTimer: ReturnType<typeof setTimeout> | null = null;

    async function copyRunId(id: string) {
        try {
            await navigator.clipboard.writeText(id);
            copiedId = true;
            if (copyTimer) clearTimeout(copyTimer);
            copyTimer = setTimeout(() => (copiedId = false), 1200);
        } catch {
            // Clipboard blocked (insecure context / denied) — leave the chip as-is.
        }
    }

    // The accent for a status that means "something to triage", or undefined
    // when there is nothing wrong. Tint is reserved for alarms (DESIGN.md) so a
    // failure is the one lit thing on the page — a deliberate stop or a skip is
    // not a failure, and success least of all. Doubles as the switch for the
    // verdict phrase's colour, so wash and wording light up together.
    function alarmAccent(displayed: string): string | undefined {
        if (
            displayed === "failed" ||
            displayed === "crashed" ||
            displayed === "log_overflow" ||
            displayed === "missed" ||
            displayed === "start_failed"
        )
            return "var(--color-danger-surface)";
        if (displayed === "timeout" || displayed === "daemon_stopped" || displayed === "queue_full")
            return "var(--color-warning-surface)";
        return undefined;
    }

    function handleConsoleKeydown(event: KeyboardEvent) {
        if (!run) return;
        if (event.key === "Escape" && consoleMaximized) {
            consoleMaximized = false;
            return;
        }
        // `F` toggles focus mode, the way a media player does — but never while
        // the operator is typing into a field (search box, etc.).
        if (
            (event.key === "f" || event.key === "F") &&
            !event.metaKey &&
            !event.ctrlKey &&
            !event.altKey
        ) {
            const target = event.target;
            if (
                target instanceof HTMLElement &&
                (target.tagName === "INPUT" ||
                    target.tagName === "TEXTAREA" ||
                    target.isContentEditable)
            )
                return;
            event.preventDefault();
            consoleMaximized = !consoleMaximized;
        }
    }

    function maximizePortal(node: HTMLElement, active: boolean) {
        const anchor = document.createComment("maximized-console");
        function moveOut() {
            const parent = node.parentNode;
            if (parent && parent !== document.body) {
                parent.insertBefore(anchor, node);
                document.body.appendChild(node);
            }
        }
        function moveBack() {
            const parent = anchor.parentNode;
            if (parent) {
                parent.insertBefore(node, anchor);
                anchor.remove();
            }
        }
        if (active) moveOut();
        return {
            update(next: boolean) {
                if (next) moveOut();
                else moveBack();
            },
            destroy() {
                // Mirror the `portal` action: if still parked on <body>, remove
                // the node so Svelte's own teardown doesn't leak it there.
                if (node.parentNode === document.body) node.remove();
                anchor.remove();
            },
        };
    }
</script>

<svelte:window onkeydown={handleConsoleKeydown} />

{#if run}
    {@const status = runDisplayStatus(run)}
    {@const config = getRunStatusConfig(status)}
    {@const DetailIcon = config.icon}
    {@const duration = runDuration(
        run,
        run.status === "running" ? durationTicker.now.getTime() : undefined,
    )}
    {@const startDelay = runStartDelay(run)}
    {@const startedAt = run.startAt ?? run.createdAt}
    {@const retry = runRetryLabel(run)}
    {@const paramEntries = run.params ? Object.entries(run.params) : []}
    {@const suffix = instanceSuffix(run.instanceIndex, getInstanceCount(run.taskName))}
    {@const spine = config.dot.replace(" animate-pulse", "")}
    {@const isRunning = run.status === "running"}
    <!-- A code is worth the ink only when it is news: `exit 0` restates
         "succeeded", while a non-zero code is the first thing to triage on. -->
    {@const showCode = status === "failed"}
    {@const verdict = runVerdict(status)}
    {@const endLabel =
        status === "stopped"
            ? "run stopped by operator"
            : status === "daemon_stopped"
              ? "daemon stopped mid-run"
              : status === "timeout"
                ? "run timed out"
                : "end of output"}
    {@const endTone =
        status === "stopped" || status === "daemon_stopped" || status === "timeout"
            ? "warn"
            : "muted"}
    {@const alarm = alarmAccent(status)}
    <!-- The panel: a status spine runs the full left edge across both the header
         readout and the console below, hugging the rail divider (artifact
         ".detail .spine"). -->
    <div class="relative flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden">
        <!-- Status spine: a vivid edge coloring the panel by outcome; it streams
             a light sweep while a run is live. -->
        <div
            class="absolute inset-y-0 left-0 z-[3] w-1 {spine} {isRunning ? 'spine-flow' : ''}"
            aria-hidden="true"
        ></div>
        <!-- Detailed header: a readout whose ink scales with how much went wrong.
             Every run states its outcome in one phrase over one quiet fact line;
             only a run worth triaging colours the phrase and tints the surface.
             Nothing here is a fixed slot — every fact renders only when it is
             true, so the header's height is itself a signal. -->
        <div
            class="head-region @container relative shrink-0 border-b border-outline-faint"
            style="--rw-oc: {alarm ?? 'var(--color-surface-raised)'}"
        >
            <div class="pt-[18px] pr-[22px] pb-[16px] pl-[26px]">
                <div class="flex items-start gap-4">
                    <div class="min-w-0 flex-1">
                        <!-- Identity, but only where the page around the panel isn't
                         already saying it: the cross-task runs list needs the task
                         name, a task's own page already has it in the breadcrumb. -->
                        {#if showTaskName || suffix}
                            <div
                                class="mb-1.5 font-mono text-[11.5px] font-medium tracking-[0.06em] text-on-surface-muted"
                            >
                                {showTaskName ? `${run.taskName}${suffix}` : `instance ${suffix}`}
                            </div>
                        {/if}

                        <!-- Verdict: the outcome as one sentence, and the only large
                         type in the panel. It is prose, so the phrase is sans and
                         only the tokens inside it — the duration, a failing exit —
                         are mono (DESIGN.md's mono-vs-sans rule). The glyph is bare
                         — no plate, no ring — so it reads as part of the sentence,
                         and it alone carries the outcome colour on a healthy run:
                         green words are ink spent on nothing being wrong. -->
                        <Tooltip content={config.description} position="right" wide>
                            <h2
                                class="flex flex-wrap items-center gap-x-2.5 text-[22px] @max-2xl:text-[18px] @max-xs:text-[16px]"
                                data-testid="run-verdict"
                                data-status={status}
                            >
                                <DetailIcon
                                    size={18}
                                    strokeWidth={2.25}
                                    class="shrink-0 {config.color} {isRunning
                                        ? 'animate-spin'
                                        : ''}"
                                />
                                <span
                                    class="font-sans font-semibold tracking-[-0.01em] {alarm
                                        ? config.color
                                        : 'text-on-surface'}">{verdict.verb}</span
                                >
                                {#if verdict.timed && duration}
                                    <span
                                        class="font-mono text-[0.86em] font-medium text-on-surface tabular-nums"
                                        data-testid="run-duration">{duration}</span
                                    >
                                {/if}
                                {#if showCode}
                                    <!-- Grouped with its own separator so a wrap can
                                     never leave the dot dangling at a line end. -->
                                    <span
                                        class="inline-flex items-center gap-x-2.5 font-mono text-[0.86em] font-medium tabular-nums"
                                    >
                                        <span class="text-on-surface-faint" aria-hidden="true"
                                            >·</span
                                        >
                                        <span class="text-danger-surface" data-testid="run-exit"
                                            >exit {run.exitCode}</span
                                        >
                                    </span>
                                {/if}
                            </h2>
                        </Tooltip>

                        <!-- Facts: when it happened and what shaped it. Queue wait,
                         retries and parameters are absent on an ordinary run and
                         appear inline the moment they exist. -->
                        <div
                            class="mt-2.5 flex flex-wrap items-center gap-x-2 gap-y-1 font-mono text-[11.5px] text-on-surface-faint tabular-nums"
                        >
                            <!-- No hover-for-the-full-timestamp here: the date sits
                             right beside the clock time, so a tooltip could only
                             repeat it. The line wraps at narrow widths instead of
                             dropping the date and hiding it behind a hover. -->
                            <span data-testid="run-started" class="text-on-surface-muted"
                                >{run.startAt ? formatClockTime(run.startAt) : "—"}</span
                            >
                            <span class="text-outline-hover" aria-hidden="true">·</span>
                            <span>{formatCalendarDate(startedAt)}</span>
                            <span class="text-outline-hover" aria-hidden="true">·</span>
                            <span>via {formatTriggeredByLabel(run.triggeredBy).toLowerCase()}</span>
                            {#if startDelay}
                                <span class="text-outline-hover" aria-hidden="true">·</span>
                                <span
                                    class="text-warning-soft-text"
                                    title="Waited between its scheduled tick and actually starting"
                                    >{startDelay} queued</span
                                >
                            {/if}
                            {#if retry}
                                <span class="text-outline-hover" aria-hidden="true">·</span>
                                <span class="inline-flex items-center gap-1">
                                    <RotateCw size={11} />
                                    retry #{run.retryAttempt}{#if run.retryOfRunId}&nbsp;of
                                        {formatShortId(run.retryOfRunId)}{/if}
                                </span>
                            {/if}
                            {#if paramEntries.length > 0}
                                <span class="text-outline-hover" aria-hidden="true">·</span>
                                <Popover placement="bottom-start">
                                    {#snippet trigger()}
                                        <span
                                            class="inline-flex cursor-pointer items-center gap-1 border-b border-dotted border-outline-hover text-on-surface-muted hover:text-primary"
                                        >
                                            <SlidersHorizontal size={11} />
                                            {paramEntries.length}
                                            {paramEntries.length === 1 ? "parameter" : "parameters"}
                                        </span>
                                    {/snippet}
                                    <div class="max-w-md min-w-48">
                                        <span
                                            class="font-mono text-xs font-semibold tracking-[0.14em] text-on-surface-faint uppercase"
                                            >Parameters</span
                                        >
                                        <dl
                                            class="mt-2 grid max-h-72 grid-cols-[max-content_1fr] gap-x-4 gap-y-1.5 overflow-y-auto"
                                        >
                                            {#each paramEntries as [key, value] (key)}
                                                <dt class="font-mono text-xs text-on-surface-muted">
                                                    {key}
                                                </dt>
                                                <dd
                                                    class="font-mono text-xs font-medium break-all text-on-surface"
                                                >
                                                    {value}
                                                </dd>
                                            {/each}
                                        </dl>
                                    </div>
                                </Popover>
                            {/if}
                            <!-- Run-id chip: click to copy the full ULID. Last, because
                             it is the fact you reach for least and the only one you
                             interact with. -->
                            <button
                                type="button"
                                onclick={() => copyRunId(run.id)}
                                title="Copy run ID"
                                class="inline-flex items-center gap-1 rounded-[3px] border border-outline-faint bg-surface-sunken px-1.5 text-on-surface-faint hover:border-outline-hover hover:text-primary"
                            >
                                {#if copiedId}
                                    <Check size={11} class="text-success-surface" />Copied
                                {:else}
                                    <Hash size={11} />{run.id}
                                {/if}
                            </button>
                        </div>
                    </div>

                    <!-- Actions: history toggle · run (split) / service lifecycle ·
                     stop · download · delete (delete behind an inline confirm) -->
                    <div class="flex shrink-0 items-center gap-2">
                        {#if onToggleHistory}
                            <button
                                type="button"
                                onclick={() => onToggleHistory()}
                                class="inline-flex items-center justify-center rounded-[3px] border border-outline-faint bg-surface-raised p-2 text-on-surface-muted hover:border-outline-hover hover:bg-surface-sunken hover:text-primary"
                                title={historyVisible ? "Hide run history" : "Show run history"}
                                aria-label={historyVisible
                                    ? "Hide run history"
                                    : "Show run history"}
                            >
                                {#if historyVisible}
                                    <PanelLeftClose size={15} />
                                {:else}
                                    <RotateCcwClock size={15} />
                                {/if}
                            </button>
                        {/if}

                        {#if onStopService || onRestartService}
                            <!-- Service lifecycle replaces the run control.
                             Restart is always available (it cancels + respawns
                             all instances in one call); Stop appears only while
                             the service is running. Each still confirms, so
                             neither fires accidentally. -->
                            {#if onRestartService}
                                <button
                                    type="button"
                                    onclick={() => onRestartService()}
                                    disabled={serviceBusy}
                                    title={serviceStopped
                                        ? "Starts the service"
                                        : "Cancels and respawns every instance"}
                                    class="inline-flex h-9 cursor-pointer items-center gap-1.5 rounded-[3px] border border-primary-soft-border bg-surface-raised px-3 font-mono text-sm font-medium text-primary hover:bg-primary-soft active:translate-y-px disabled:cursor-not-allowed disabled:opacity-60"
                                >
                                    {#if serviceStopped}
                                        <Play size={15} fill="currentColor" stroke="none" />
                                    {:else}
                                        <RefreshCcw size={15} />
                                    {/if}
                                    <span class="@max-md:hidden"
                                        >{serviceStopped ? "Start" : "Restart"}</span
                                    >
                                </button>
                            {/if}
                            {#if !serviceStopped && onStopService}
                                <button
                                    type="button"
                                    onclick={() => onStopService()}
                                    disabled={serviceBusy}
                                    title="Stops the service for the rest of the daemon's lifetime"
                                    class="inline-flex h-9 cursor-pointer items-center gap-1.5 rounded-[3px] border border-danger-soft-border bg-surface-raised px-3 font-mono text-sm font-medium text-danger-surface hover:bg-danger-soft active:translate-y-px disabled:cursor-not-allowed disabled:opacity-60"
                                >
                                    <Square size={15} fill="currentColor" stroke="none" />
                                    <span class="@max-md:hidden">Stop Service</span>
                                </button>
                            {/if}
                        {:else}
                            {#if onRun}
                                <!-- Split button: Run (defaults) + an arrow for the
                                 reuse-parameters variant, shown only when there is one. -->
                                <div
                                    class="inline-flex overflow-hidden rounded-[3px] border border-primary-soft-border"
                                >
                                    <button
                                        type="button"
                                        onclick={() => onRun()}
                                        title="Run this task now with default options"
                                        class="inline-flex h-9 cursor-pointer items-center gap-1.5 bg-surface-raised px-3 font-mono text-sm font-medium text-primary hover:bg-primary-soft active:translate-y-px"
                                    >
                                        <Play size={15} fill="currentColor" stroke="none" />
                                        <span class="@max-md:hidden">Run</span>
                                    </button>
                                    {#if onRunAgain}
                                        <Popover bind:open={runMenuOpen} placement="bottom-end">
                                            {#snippet trigger()}
                                                <span
                                                    class="flex h-9 cursor-pointer items-center border-l border-primary-soft-border bg-surface-raised px-2 text-primary hover:bg-primary-soft"
                                                    title="More run options"
                                                    aria-label="More run options"
                                                >
                                                    <ChevronDown size={15} />
                                                </span>
                                            {/snippet}
                                            <button
                                                type="button"
                                                onclick={() => {
                                                    runMenuOpen = false;
                                                    onRunAgain?.();
                                                }}
                                                class="flex w-56 items-start gap-2.5 text-left"
                                            >
                                                <RotateCw
                                                    size={15}
                                                    class="mt-0.5 shrink-0 text-on-surface-muted"
                                                />
                                                <span class="flex flex-col">
                                                    <span
                                                        class="font-mono text-sm font-medium text-on-surface"
                                                        >Run again</span
                                                    >
                                                    <span class="text-xs text-on-surface-muted"
                                                        >Reuse this run's parameters</span
                                                    >
                                                </span>
                                            </button>
                                        </Popover>
                                    {/if}
                                </div>
                            {/if}
                            {#if isRunning && onStop}
                                <button
                                    type="button"
                                    onclick={() => onStop(run.id)}
                                    class="inline-flex h-9 cursor-pointer items-center gap-1.5 rounded-[3px] border border-danger-soft-border bg-surface-raised px-3 font-mono text-sm font-medium text-danger-surface hover:bg-danger-soft active:translate-y-px"
                                >
                                    <Square size={15} fill="currentColor" stroke="none" />
                                    <span class="@max-md:hidden">Stop</span>
                                </button>
                            {/if}
                        {/if}
                        <a
                            href="/api/tasks/{encodeURIComponent(
                                run.taskName,
                            )}/runs/{encodeURIComponent(run.id)}/log/raw"
                            download="{run.taskName}-{run.id}.log"
                            class="inline-flex items-center justify-center rounded-[3px] border border-outline-faint bg-surface-raised p-2 text-on-surface-muted hover:border-outline-hover hover:bg-surface-sunken hover:text-primary"
                            title="Download the full log (rotated and current parts as one file)"
                            aria-label="Download log"
                        >
                            <Download size={15} />
                        </a>
                        {#if canDelete}
                            <Popover bind:open={confirmDeleteOpen} placement="bottom-end">
                                {#snippet trigger()}
                                    <span
                                        class="flex cursor-pointer items-center justify-center rounded-[3px] border border-outline-faint bg-surface-raised p-2 text-on-surface-muted hover:border-danger-soft-border hover:bg-danger-soft hover:text-danger-surface"
                                        title="Delete this run"
                                        aria-label="Delete run"
                                    >
                                        <Trash2 size={15} />
                                    </span>
                                {/snippet}
                                <div class="w-60">
                                    <div class="font-mono text-sm font-semibold text-on-surface">
                                        Delete this run?
                                    </div>
                                    <div class="mt-1 text-xs leading-relaxed text-on-surface-muted">
                                        Its captured output is removed from disk. This can't be
                                        undone.
                                    </div>
                                    <div class="mt-3 flex justify-end gap-2">
                                        <Button
                                            variant="ghost"
                                            size="sm"
                                            onclick={() => (confirmDeleteOpen = false)}
                                        >
                                            Cancel
                                        </Button>
                                        <Button
                                            variant="danger"
                                            size="sm"
                                            onclick={() => {
                                                confirmDeleteOpen = false;
                                                onDelete?.(run.id);
                                            }}
                                        >
                                            Delete run
                                        </Button>
                                    </div>
                                </div>
                            </Popover>
                        {/if}
                    </div>
                </div>
            </div>
        </div>

        <!-- Console hero — maximizes to a full-bleed overlay via portal -->
        {#if consoleMaximized}
            <div
                use:portal
                class="console-backdrop fixed inset-0 z-40 bg-backdrop backdrop-blur-sm"
                onclick={() => (consoleMaximized = false)}
                role="presentation"
            ></div>
        {/if}
        <div
            use:maximizePortal={consoleMaximized}
            class={consoleMaximized
                ? "console-zoom fixed inset-3 z-50 flex flex-col overflow-hidden rounded-[4px] bg-[var(--rw-con-bg)] shadow-2xl ring-1 ring-[var(--rw-con-gutter)] sm:inset-6"
                : "ml-1 flex min-h-[300px] flex-1 flex-col overflow-hidden border-t border-[var(--rw-con-gutter)] bg-[var(--rw-con-bg)]"}
        >
            <div
                class="flex shrink-0 items-center justify-between gap-3 border-b border-[var(--rw-con-gutter)] bg-[var(--rw-con-panel)] px-3.5 py-[9px] font-mono text-[11.5px] text-[var(--rw-con-dim)]"
            >
                <div class="flex min-w-0 items-center gap-3">
                    {#if consoleMaximized}
                        <!-- Focus-mode identity: which run is filling the screen -->
                        <span class="flex min-w-0 items-center gap-2">
                            <span class="h-2 w-2 shrink-0 rounded-full {spine}"></span>
                            <span class="truncate font-medium text-[var(--rw-con-text)]">
                                {run.taskName}{suffix}
                            </span>
                            <span class="text-[var(--rw-con-gutter)]">·</span>
                            <span class="font-semibold capitalize {config.color}">{status}</span>
                        </span>
                    {:else}
                        <span
                            class="flex items-center gap-2 font-semibold text-[var(--rw-con-text)]"
                        >
                            <TerminalIcon size={14} class="opacity-70" />
                            Console output
                        </span>
                        <span class="hidden text-[var(--rw-con-gutter)] sm:inline"
                            >stdout + stderr</span
                        >
                    {/if}
                </div>
                <div class="flex shrink-0 items-center gap-3">
                    <button
                        type="button"
                        onclick={() => (consoleWrap = !consoleWrap)}
                        class="flex cursor-pointer items-center gap-1.5 rounded-[3px] px-2 py-1 hover:bg-[var(--rw-con-gutter)]/30 hover:text-[var(--rw-con-text)] {consoleWrap
                            ? 'text-[var(--rw-con-text)]'
                            : 'text-[var(--rw-con-dim)]'}"
                        title={consoleWrap ? "Unwrap lines" : "Wrap long lines"}
                        aria-pressed={consoleWrap}
                        aria-label="Toggle line wrapping"
                    >
                        <TextWrap size={13} />
                        <span class="hidden sm:inline">Wrap</span>
                    </button>
                    <button
                        type="button"
                        onclick={() => (consoleMaximized = !consoleMaximized)}
                        class="flex cursor-pointer items-center gap-1.5 rounded-[3px] px-2 py-1 text-[var(--rw-con-dim)] hover:bg-[var(--rw-con-gutter)]/30 hover:text-[var(--rw-con-text)]"
                        title={consoleMaximized ? "Restore console (Esc)" : "Maximize console · F"}
                        aria-label={consoleMaximized ? "Restore console" : "Maximize console"}
                    >
                        {#if consoleMaximized}
                            <Minimize2 size={13} />
                            <span
                                class="rounded-[3px] border border-[var(--rw-con-gutter)] px-1.5 text-[10px] tracking-wide"
                                >Esc</span
                            >
                        {:else}
                            <Maximize2 size={13} />
                            Expand
                        {/if}
                    </button>
                </div>
            </div>
            {#key run.id}
                <LogConsole
                    bind:this={logConsole}
                    fetchLogs={(f: number, t: number) => fetchLogs(run.id, f, t)}
                    fetchLineHistory={fetchLineHistory
                        ? (n: number) => fetchLineHistory(run.id, n)
                        : undefined}
                    bind:wrap={consoleWrap}
                    class="min-h-0 flex-1"
                    {highlightLine}
                    {endLabel}
                    {endTone}
                />
            {/key}
        </div>
    </div>
{:else}
    <div
        class="flex min-w-0 flex-1 flex-col items-center justify-center gap-4 bg-surface-sunken/30"
    >
        <EmptyState
            title={notFound ? "Run not found" : onRunTask ? "No runs yet" : "Select a run"}
            description={notFound
                ? "This run doesn't exist — it may have been deleted by retention, or the link is wrong. Pick a run from the list to continue."
                : onRunTask
                  ? "This task hasn't run yet. Trigger it to see its output here."
                  : "Pick a run from the list to view details and logs."}
            icon={notFound ? SearchX : MousePointerClick}
        />
        {#if onRunTask && !notFound}
            <button
                type="button"
                onclick={() => onRunTask()}
                class="inline-flex cursor-pointer items-center gap-1.5 rounded-[3px] border border-primary-soft-border bg-surface-raised px-3 py-2 font-mono text-sm font-medium text-primary hover:bg-primary-soft active:translate-y-px"
            >
                <Play size={15} fill="currentColor" stroke="none" />
                Run task
            </button>
        {/if}
    </div>
{/if}

<style>
    /* Only a run worth triaging is washed in its outcome colour (--rw-oc, set
       inline). Everything else — success included — passes the surface itself so
       the mix collapses to a plain surface, which is what lets a genuine failure
       be the one lit thing on the page. */
    .head-region {
        background: color-mix(in srgb, var(--rw-oc) 7%, var(--color-surface-raised));
    }

    /* Gentle fade for the maximize backdrop; disabled under reduced motion. */
    .console-backdrop {
        animation: console-backdrop-in 150ms;
    }
    @keyframes console-backdrop-in {
        from {
            opacity: 0;
        }
        to {
            opacity: 1;
        }
    }

    /* The console zooms up as it takes over the viewport. */
    .console-zoom {
        animation: console-zoom-in 260ms cubic-bezier(0.33, 0.05, 0.2, 1);
    }
    @keyframes console-zoom-in {
        from {
            opacity: 0.4;
            transform: scale(0.96);
        }
        to {
            opacity: 1;
            transform: none;
        }
    }

    /* Living spine: a light sweep travels down the edge while a run is in flight. */
    .spine-flow {
        overflow: hidden;
    }
    .spine-flow::after {
        content: "";
        position: absolute;
        inset: 0;
        height: 40%;
        background: linear-gradient(
            180deg,
            transparent,
            color-mix(in srgb, #fff 70%, transparent),
            transparent
        );
        animation: spine-flow 1.9s linear infinite;
    }
    @keyframes spine-flow {
        from {
            transform: translateY(-100%);
        }
        to {
            transform: translateY(300%);
        }
    }

    @media (prefers-reduced-motion: reduce) {
        .console-backdrop,
        .console-zoom,
        .spine-flow::after {
            animation: none;
        }
    }
</style>
