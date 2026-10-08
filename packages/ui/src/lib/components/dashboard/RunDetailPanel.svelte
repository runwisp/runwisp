<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import {
        Terminal as TerminalIcon,
        Play,
        MousePointerClick,
        ArrowLeft,
        PanelLeftClose,
        PanelLeftOpen,
        SearchX,
    } from "@lucide/svelte";
    import EmptyState from "../EmptyState.svelte";
    import LogConsole from "../LogConsole.svelte";
    import Tooltip from "../Tooltip.svelte";
    import { prefersReducedMotion } from "../../actions/row-motion.js";
    import type { RunMotion } from "../../utils/run-motion.js";
    import type { LogEvent, LogSlice } from "../../log-console/types.js";
    import { TickingNow } from "../../utils/ticking-now.svelte.js";
    import { displayStatus, type ResourceUsage, type Run } from "@runwisp/common";
    import RunActions from "./RunActions.svelte";
    import RunConsoleFrame from "./RunConsoleFrame.svelte";
    import RunFacts from "./RunFacts.svelte";
    import { RUN_STATUS_CONFIG } from "./status-config.js";
    import { runDuration, runVerdict, instanceSuffix } from "./run-helpers.js";

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
        onBack,
        onToggleList,
        listVisible = true,
        highlightLine = null,
        getInstanceCount = () => 1,
        getLiveUsage = () => undefined,
        motion,
        notFound = false,
        loading = false,
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
        // Trigger the task from the empty state (no run selected yet), the
        // cold-start path so a never-run task is still launchable from here.
        onRunTask?: (() => void) | undefined;
        // Service lifecycle controls. When either is set the cluster swaps the
        // Run control for Stop / Restart (chosen by serviceStopped).
        onStopService?: (() => void) | undefined;
        onRestartService?: (() => void) | undefined;
        serviceStopped?: boolean;
        serviceBusy?: boolean;
        // Return to the run list when the panel replaces it (a phone), shown as
        // a back arrow in the header's top-left corner.
        onBack?: (() => void) | undefined;
        // Fold the run list away (or back) when it sits beside the panel on a
        // screen too narrow to keep both comfortably, in the same corner.
        onToggleList?: (() => void) | undefined;
        listVisible?: boolean;
        highlightLine?: number | null;
        // Resolves a task's currently configured instance count so multi-instance
        // services render a 1-based #N suffix. Defaults to single-instance.
        getInstanceCount?: (taskName: string) => number;
        // Live CPU and memory of a running run, by run ID; undefined when it
        // isn't measured. Shown in the header while the run is running.
        getLiveUsage?: (runId: string) => ResourceUsage | undefined;
        // Which runs arrived or were removed live moments ago. The panel eases in
        // when a new run takes it (triggered, or a scheduled run auto-selected)
        // or when its run was deleted and another takes its place; picking a
        // run by hand swaps instantly.
        motion?: RunMotion;
        // True when a deep-linked run id resolved to no run (deleted by retention,
        // or never existed). The empty state then says so plainly instead of the
        // generic "Select a run", the caller must not silently substitute another.
        notFound?: boolean;
        // True while there is no run to show *yet* (the run list or a deep-linked
        // run is still loading), so the empty state doesn't claim "No runs yet".
        loading?: boolean;
    } = $props();

    function easeInFresh(node: HTMLElement, runId: string) {
        let shown = runId;
        const play = (id: string) => {
            const previous = shown;
            shown = id;
            if (!motion?.arrived(id) && !motion?.removed(previous)) return;
            if (prefersReducedMotion()) return;
            node.animate(
                [
                    { opacity: 0.35, translate: "0 6px" },
                    { opacity: 1, translate: "0 0" },
                ],
                { duration: 200, easing: "cubic-bezier(0.2, 0.8, 0.2, 1)" },
            );
        };
        play(runId);
        return { update: play };
    }

    let canDelete = $derived.by(() => {
        if (!run || !onDelete) return false;
        const status = displayStatus(run.status, run.endReason);
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
</script>

{#if run}
    {@const status = displayStatus(run.status, run.endReason)}
    {@const config = RUN_STATUS_CONFIG[status]}
    {@const DetailIcon = config.icon}
    {@const duration = runDuration(
        run,
        run.status === "running" ? durationTicker.now.getTime() : undefined,
    )}
    {@const suffix = instanceSuffix(run.instanceIndex, getInstanceCount(run.taskName))}
    {@const spine = config.solidDot}
    {@const isRunning = run.status === "running"}
    <!-- A code is worth the ink only when it is news: `exit 0` restates
         "succeeded", while a non-zero code is the first thing to triage on. -->
    {@const showCode = status === "failed"}
    {@const verdict = runVerdict(status)}
    {@const alarm = config.alarm}
    <!-- The panel: a status spine runs the full left edge across both the header
         readout and the console below, hugging the rail divider (artifact
         ".detail .spine"). -->
    <div
        use:easeInFresh={run.id}
        class="relative flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden"
    >
        <!-- Status spine: a vivid edge coloring the panel by outcome; it streams
             a light sweep while a run is live. -->
        <div
            class="absolute inset-y-0 left-0 z-[3] w-1 {spine} {isRunning ? 'spine-flow' : ''}"
            aria-hidden="true"
        ></div>
        <!-- Detailed header: a readout whose ink scales with how much went wrong.
             Every run states its outcome in one phrase over one quiet fact line;
             only a run worth triaging colours the phrase and tints the surface.
             Nothing here is a fixed slot, every fact renders only when it is
             true, so the header's height is itself a signal. -->
        <div
            class="head-region @container relative shrink-0 border-b border-outline-faint"
            style="--rw-oc: {alarm ?? 'var(--color-surface-raised)'}"
        >
            <div class="pt-[18px] pr-[22px] pb-[16px] pl-[26px]">
                <!-- The actions wrap onto their own row rather than squeeze the
                     readout below a legible width on a narrow panel. -->
                <div class="flex flex-wrap items-start gap-x-3 gap-y-3">
                    {#if onBack}
                        <button
                            type="button"
                            onclick={() => onBack()}
                            class="-mt-1 -ml-2 shrink-0 rounded-[3px] p-1.5 text-on-surface-muted hover:bg-surface-sunken hover:text-primary"
                            title="Back to runs"
                            aria-label="Back to runs"
                        >
                            <ArrowLeft size={20} />
                        </button>
                    {:else if onToggleList}
                        <button
                            type="button"
                            onclick={() => onToggleList()}
                            class="-mt-1 -ml-2 shrink-0 rounded-[3px] p-1.5 text-on-surface-muted hover:bg-surface-sunken hover:text-primary"
                            title={listVisible ? "Hide run list" : "Show run list"}
                            aria-label={listVisible ? "Hide run list" : "Show run list"}
                            aria-expanded={listVisible}
                        >
                            {#if listVisible}
                                <PanelLeftClose size={20} />
                            {:else}
                                <PanelLeftOpen size={20} />
                            {/if}
                        </button>
                    {/if}
                    <div class="min-w-60 flex-1">
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
                         only the tokens inside it, the duration, a failing exit,
                         are mono (DESIGN.md's mono-vs-sans rule). The glyph is bare
                         (no plate, no ring) so it reads as part of the sentence,
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

                        <RunFacts {run} live={isRunning ? getLiveUsage(run.id) : undefined} />
                    </div>

                    <RunActions
                        runId={run.id}
                        taskName={run.taskName}
                        {isRunning}
                        {canDelete}
                        {serviceStopped}
                        {serviceBusy}
                        {onRun}
                        {onRunAgain}
                        {onStop}
                        {onStopService}
                        {onRestartService}
                        {onDelete}
                    />
                </div>
            </div>
        </div>

        <RunConsoleFrame
            {run}
            {suffix}
            {fetchLogs}
            {streamLogs}
            {fetchLineHistory}
            {highlightLine}
        />
    </div>
{:else if loading && !notFound}
    <!-- Loading: the panel's own frame (verdict header over the console) with
         placeholders where the run's facts go, so nothing jumps when it lands. -->
    <div
        class="relative flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden"
        role="status"
        aria-label="Loading run"
    >
        <div class="shrink-0 border-b border-outline-faint bg-surface-raised">
            <div class="pt-[18px] pr-[22px] pb-[16px] pl-[26px]">
                <div class="flex h-[33px] items-center gap-2.5">
                    <span class="size-[18px] animate-pulse rounded-full bg-outline-hover"></span>
                    <span class="h-[18px] w-40 animate-pulse rounded-[3px] bg-outline-hover"></span>
                </div>
                <div class="mt-2.5 flex h-[17px] items-center">
                    <span class="h-3 w-64 max-w-full animate-pulse rounded-[3px] bg-outline-hover"
                    ></span>
                </div>
            </div>
        </div>
        <div
            class="ml-1 flex min-h-[300px] flex-1 flex-col overflow-hidden border-t border-[var(--rw-con-gutter)] bg-[var(--rw-con-bg)]"
        >
            <div
                class="flex shrink-0 items-center gap-2 border-b border-[var(--rw-con-gutter)] bg-[var(--rw-con-panel)] px-3.5 py-[9px] font-mono text-[11.5px] font-semibold text-[var(--rw-con-text)]"
            >
                <TerminalIcon size={14} class="opacity-70" />
                Console output
            </div>
            <LogConsole fetchLogs={() => undefined} loading class="min-h-0 flex-1" />
        </div>
    </div>
{:else}
    <div
        class="flex min-w-0 flex-1 flex-col items-center justify-center gap-4 bg-surface-sunken/30"
    >
        <EmptyState
            title={notFound ? "Run not found" : onRunTask ? "No runs yet" : "Select a run"}
            description={notFound
                ? "This run doesn't exist. It may have been deleted by retention, or the link is wrong. Pick a run from the list to continue."
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
       inline). Everything else, success included, passes the surface itself so
       the mix collapses to a plain surface, which is what lets a genuine failure
       be the one lit thing on the page. */
    .head-region {
        background: color-mix(in srgb, var(--rw-oc) 7%, var(--color-surface-raised));
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
        .spine-flow::after {
            animation: none;
        }
    }
</style>
