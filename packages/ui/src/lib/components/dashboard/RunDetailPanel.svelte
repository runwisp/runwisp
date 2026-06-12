<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { untrack } from "svelte";
    import {
        Server,
        Hash,
        Terminal as TerminalIcon,
        Download,
        Trash2,
        MousePointerClick,
    } from "@lucide/svelte";
    import Badge from "../Badge.svelte";
    import Button from "../Button.svelte";
    import EmptyState from "../EmptyState.svelte";
    import LogConsole from "../LogConsole.svelte";
    import Tooltip from "../Tooltip.svelte";
    import type { Run } from "./types.js";
    import type { LogEvent, LogSlice } from "../../log-console/types.js";
    import { formatDateTime } from "../../utils/format.js";
    import { formatShortId } from "../../utils/id.js";
    import { getRunStatusConfig, runDisplayStatus } from "./status-config.js";
    import { runDuration, runStartDelay } from "./run-helpers.js";

    let {
        run,
        fetchLogs,
        streamLogs,
        showTaskName = false,
        onDelete,
        highlightLine = null,
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
        showTaskName?: boolean;
        onDelete?: (runId: string) => void;
        highlightLine?: number | null;
    } = $props();

    let canDelete = $derived.by(() => {
        if (!run || !onDelete) return false;
        const status = runDisplayStatus(run);
        return status !== "running" && status !== "pending";
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

        untrack(() => {
            // Single SSE call seeds the viewport via backfill (negative
            // fromLine = tail-from-end) and continues with live events on
            // the same connection. No tail+stream handoff, no duplicate
            // replay, native Last-Event-ID resume on reconnect.
            cleanup = stream(
                id,
                (event: LogEvent) => {
                    if (logConsole) logConsole.onStream(event);
                },
                { fromLine: -TAIL_LINES },
            );
        });

        return () => {
            if (cleanup) cleanup();
        };
    });
</script>

{#if run}
    {@const config = getRunStatusConfig(runDisplayStatus(run))}
    {@const DetailIcon = config.icon}
    {@const duration = runDuration(run)}
    {@const startDelay = runStartDelay(run)}
    <!-- Detailed Header -->
    <div class="@container shrink-0 border-b border-outline-faint bg-surface-raised p-6">
        <div class="flex flex-col justify-between gap-6 @4xl:flex-row @4xl:items-start">
            <div class="flex items-start gap-4">
                <!-- Large Status Icon -->
                <div
                    class="flex h-14 w-14 shrink-0 items-center justify-center rounded-2xl {config.bg} {config.color}"
                >
                    <DetailIcon size={32} class={run.status === "running" ? "animate-spin" : ""} />
                </div>

                <div>
                    <div class="mb-1 flex items-center gap-3">
                        <h2 class="text-xl font-bold text-on-surface">
                            {#if showTaskName}
                                {run.task_name}{#if run.instance_index > 0}<span
                                        class="text-on-surface-muted">#{run.instance_index}</span
                                    >{/if}
                            {:else}
                                Run #{formatShortId(run.id)}{#if run.instance_index > 0}
                                    <span class="text-on-surface-muted"
                                        >· instance #{run.instance_index}</span
                                    >
                                {/if}
                            {/if}
                        </h2>
                        <Tooltip content={config.description} position="bottom" wide>
                            <Badge
                                variant={runDisplayStatus(run) === "success"
                                    ? "success"
                                    : run.status === "running"
                                      ? "info"
                                      : "danger"}
                            >
                                {runDisplayStatus(run).toUpperCase()}
                            </Badge>
                        </Tooltip>
                        {#if canDelete}
                            <Button
                                variant="ghost"
                                size="xs"
                                class="ml-1 text-danger-surface hover:bg-danger-soft"
                                onclick={() => onDelete?.(run.id)}
                                title="Delete this run"
                                aria-label="Delete run"
                            >
                                {#snippet icon()}<Trash2 size={14} />{/snippet}
                            </Button>
                        {/if}
                    </div>
                    <div
                        class="flex flex-wrap items-center gap-x-4 gap-y-2 text-sm text-on-surface-muted"
                    >
                        {#if showTaskName}
                            <div
                                class="flex items-center gap-1.5 font-mono text-xs text-on-surface-faint"
                            >
                                <Hash size={12} />
                                Run ID: <span class="text-on-surface-muted">{run.id}</span>
                            </div>
                            <div
                                class="hidden h-1 w-1 rounded-full bg-outline-faint sm:block"
                            ></div>
                            <div class="flex items-center gap-1.5">
                                <Server size={14} />
                                <span
                                    >Triggered by <span class="font-medium text-on-surface"
                                        >{run.triggered_by}</span
                                    ></span
                                >
                            </div>
                        {:else}
                            <div class="flex items-center gap-1.5">
                                <Server size={14} />
                                <span
                                    >Triggered by <span class="font-medium text-on-surface"
                                        >{run.triggered_by}</span
                                    ></span
                                >
                            </div>
                            <div
                                class="hidden h-1 w-1 rounded-full bg-outline-faint sm:block"
                            ></div>
                            <div
                                class="flex items-center gap-1.5 font-mono text-xs text-on-surface-faint"
                            >
                                <Hash size={12} />
                                {run.id}
                            </div>
                        {/if}
                    </div>
                </div>
            </div>

            <!-- Stats Grid -->
            <div
                class="grid grid-cols-1 gap-3 rounded-xl border border-outline-faint bg-surface-sunken px-6 py-3 @lg:grid-cols-3 @lg:gap-6 @4xl:shrink-0"
            >
                <div class="flex flex-col gap-1">
                    <span
                        class="text-xs font-semibold tracking-wide text-on-surface-faint uppercase"
                        >Started</span
                    >
                    <span class="font-mono text-sm font-medium whitespace-nowrap text-on-surface">
                        {run.start_at ? formatDateTime(run.start_at) : "--:--"}
                    </span>
                    {#if startDelay}
                        <span
                            class="font-mono text-xs whitespace-nowrap text-on-surface-faint"
                            title="Scheduled at {formatDateTime(run.created_at)}"
                        >
                            +{startDelay} after scheduled
                        </span>
                    {/if}
                </div>

                <div
                    class="flex flex-col gap-1 border-t border-outline pt-3 @lg:border-t-0 @lg:border-l @lg:pt-0 @lg:pl-6"
                >
                    <span
                        class="text-xs font-semibold tracking-wide text-on-surface-faint uppercase"
                        >Duration</span
                    >
                    <span class="font-mono text-sm font-medium text-on-surface">
                        {duration ?? "--"}
                    </span>
                </div>

                <div
                    class="flex flex-col gap-1 border-t border-outline pt-3 @lg:border-t-0 @lg:border-l @lg:pt-0 @lg:pl-6"
                >
                    <span
                        class="text-xs font-semibold tracking-wide text-on-surface-faint uppercase"
                        >Exited</span
                    >
                    <span
                        class="text-sm font-medium {run.exit_code === 0
                            ? 'text-success-surface'
                            : 'text-on-surface'} font-mono"
                    >
                        {typeof run.exit_code === "number" ? `Code ${run.exit_code}` : "-"}
                    </span>
                </div>
            </div>
        </div>
    </div>

    <!-- Console View -->
    <div class="flex min-h-0 flex-1 flex-col bg-mist-950">
        <div
            class="flex shrink-0 items-center justify-between border-b border-mist-800 bg-mist-900/50 px-4 py-2 font-mono text-xs text-mist-400"
        >
            <div class="flex items-center gap-2">
                <TerminalIcon size={14} />
                <span>Console Output</span>
            </div>
            <div class="flex items-center gap-3">
                {#if run.status === "running"}
                    <div class="text-info-surface flex items-center gap-1.5">
                        <div class="bg-info-surface h-1.5 w-1.5 animate-pulse rounded-full"></div>
                        Live
                    </div>
                {:else if run.status === "pending"}
                    <div class="flex items-center gap-1.5 text-warning-surface">
                        <div
                            class="h-1.5 w-1.5 animate-pulse rounded-full bg-warning-surface"
                        ></div>
                        Pending
                    </div>
                {/if}
                <a
                    href="/api/tasks/{encodeURIComponent(run.task_name)}/runs/{encodeURIComponent(
                        run.id,
                    )}/log/raw"
                    download="{run.task_name}-{run.id}.log"
                    class="flex items-center gap-1.5 rounded border border-mist-700 px-2 py-1 text-mist-200 transition-colors hover:border-mist-500 hover:bg-mist-800 hover:text-mist-100"
                    title="Download the full log (rotated and current parts as one file)"
                >
                    <Download size={12} />
                    Download
                </a>
            </div>
        </div>
        {#key run.id}
            <LogConsole
                bind:this={logConsole}
                fetchLogs={(f: number, t: number) => fetchLogs(run.id, f, t)}
                class="min-h-0 flex-1"
                {highlightLine}
            />
        {/key}
    </div>
{:else}
    <div class="flex flex-1 items-center justify-center bg-surface-sunken/30">
        <EmptyState
            title="Select a run"
            description="Pick a run from the list to view details and logs."
            icon={MousePointerClick}
        />
    </div>
{/if}
