<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { untrack } from "svelte";
    import { Server, Hash, Terminal as TerminalIcon } from "@lucide/svelte";
    import Badge from "../Badge.svelte";
    import LogConsole from "../LogConsole.svelte";
    import type { Run } from "./types.js";
    import type { LogEvent, LogSlice } from "../../log-console/types.js";
    import { formatDateTime } from "../../utils/format.js";
    import { formatShortId } from "../../utils/id.js";
    import { getRunStatusConfig, runDisplayStatus } from "./status-config.js";
    import { runDuration } from "./run-helpers.js";

    let {
        run,
        fetchLogs,
        streamLogs,
        showTaskName = false,
    }: {
        run: Run | undefined;
        fetchLogs: (
            runId: string,
            from: number,
            to: number,
        ) => Promise<LogSlice | LogEvent | undefined> | LogSlice | LogEvent | undefined;
        streamLogs?: (runId: string, onEvent: (event: LogEvent) => void) => () => void;
        showTaskName?: boolean;
    } = $props();

    let logConsole = $state<{ onStream: (event: LogEvent) => void } | null>(null);

    // Derive a stable scalar so the effect only re-runs when the id actually
    // changes, not on every run object reference swap from SSE array updates.
    let runId = $derived(run?.id);

    $effect(() => {
        const id = runId;
        if (!id || !streamLogs) return;

        const connect = streamLogs;
        const unsub = untrack(() =>
            connect(id, (event: LogEvent) => {
                if (logConsole) logConsole.onStream(event);
            }),
        );

        return () => {
            unsub();
        };
    });
</script>

{#if run}
    {@const config = getRunStatusConfig(runDisplayStatus(run))}
    {@const DetailIcon = config.icon}
    {@const duration = runDuration(run)}
    <!-- Detailed Header -->
    <div class="shrink-0 border-b border-outline-faint bg-surface-raised p-6">
        <div class="flex flex-col justify-between gap-6 lg:flex-row lg:items-start">
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
                                {run.task_name}{#if run.replica_index > 0}<span
                                        class="text-on-surface-muted">#{run.replica_index}</span
                                    >{/if}
                            {:else}
                                Run #{formatShortId(run.id)}{#if run.replica_index > 0}
                                    <span class="text-on-surface-muted"
                                        >· replica #{run.replica_index}</span
                                    >
                                {/if}
                            {/if}
                        </h2>
                        <Badge
                            variant={runDisplayStatus(run) === "success"
                                ? "success"
                                : run.status === "running"
                                  ? "info"
                                  : "danger"}
                        >
                            {runDisplayStatus(run).toUpperCase()}
                        </Badge>
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
                class="grid grid-cols-3 gap-6 rounded-xl border border-outline-faint bg-surface-sunken px-6 py-3"
            >
                <div class="flex flex-col gap-1">
                    <span
                        class="text-xs font-semibold tracking-wide text-on-surface-faint uppercase"
                        >Started</span
                    >
                    <span class="font-mono text-sm font-medium whitespace-nowrap text-on-surface">
                        {run.start_at ? formatDateTime(run.start_at) : "--:--"}
                    </span>
                </div>

                <div class="flex flex-col gap-1 border-l border-outline pl-6">
                    <span
                        class="text-xs font-semibold tracking-wide text-on-surface-faint uppercase"
                        >Duration</span
                    >
                    <span class="font-mono text-sm font-medium text-on-surface">
                        {duration ?? "--"}
                    </span>
                </div>

                <div class="flex flex-col gap-1 border-l border-outline pl-6">
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
    <div class="flex min-h-0 flex-1 flex-col bg-slate-950">
        <div
            class="flex shrink-0 items-center justify-between border-b border-slate-800 bg-slate-900/50 px-4 py-2 font-mono text-xs text-slate-400"
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
            </div>
        </div>
        {#key run.id}
            <LogConsole
                bind:this={logConsole}
                fetchLogs={(f: number, t: number) => fetchLogs(run.id, f, t)}
                class="min-h-0 flex-1"
            />
        {/key}
    </div>
{:else}
    <div
        class="flex flex-1 flex-col items-center justify-center bg-surface-sunken/30 text-on-surface-faint"
    >
        <div class="mb-4 rounded-full border border-outline bg-surface-raised p-4 shadow-sm">
            <Server size={32} class="text-outline-faint" />
        </div>
        <h3 class="mb-1 font-medium text-on-surface">No Run Selected</h3>
        <p class="text-sm">Select a run from the history on the left to view details.</p>
    </div>
{/if}
