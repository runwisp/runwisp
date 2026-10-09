<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { displayStatus, type Run } from "@runwisp/common";
    import { RUN_STATUS_CONFIG } from "./status-config.js";
    import {
        formatTriggeredByLabel,
        runRetryLabel,
        runRowReadout,
        runUsageAmounts,
        runUsageLabel,
        usageLevel,
        highlightParts,
        type RunUsageScale,
    } from "./run-helpers.js";
    import type { RunOutputMatch } from "./types.js";
    import {
        formatDateTime,
        formatFullDateTime,
        formatTimeHM,
        formatDayMonth,
        formatBytes,
        formatDuration,
    } from "../../utils/format.js";

    let {
        run,
        active,
        suffix,
        showTaskName,
        match,
        outputQuery,
        bulkActions,
        selectionActive,
        usage,
        onselect,
    }: {
        run: Run;
        active: boolean;
        /** Instance suffix of a multi-instance service ("" otherwise). */
        suffix: string;
        showTaskName: boolean;
        /** First output line that matched the active search, if any. */
        match: RunOutputMatch | undefined;
        outputQuery: string;
        bulkActions: boolean;
        selectionActive: boolean;
        /**
         * The task's usage scale when the rail shows CPU/RAM bars, null while
         * no run has usage yet (rows keep the bars' room), undefined for none.
         */
        usage: RunUsageScale | null | undefined;
        onselect: (runId: string) => void;
    } = $props();

    const dstatus = $derived(displayStatus(run.status, run.endReason));
    const config = $derived(RUN_STATUS_CONFIG[dstatus]);
    const running = $derived(run.status === "running");
    const startedAt = $derived(run.startedAt ?? run.createdAt);
    const retry = $derived(runRetryLabel(run));
    const amounts = $derived(usage ? runUsageAmounts(run) : undefined);
    const LEVEL_COLOR = ["bg-success-surface", "bg-warning-surface", "bg-danger-surface"];
    // With bulk actions on, the status dot fades out on row hover, or whenever a
    // selection exists, so the row checkbox can take its place over it.
    const dotFade = $derived(
        bulkActions ? (selectionActive ? "opacity-0" : "group-hover/row:opacity-0") : "",
    );
</script>

<button
    class="btn-scale group relative w-full rounded-[3px] border text-left select-none {showTaskName
        ? 'p-3'
        : 'px-3 py-[11px]'} {active
        ? 'border-outline bg-surface-raised shadow-sm'
        : 'border-transparent hover:border-outline-hover hover:bg-surface-sunken'}"
    onclick={() => onselect(run.id)}
>
    {#if showTaskName}
        <!-- Cross-task /runs variant: the same readout language as the task
                 rail, status dot, status-colored outcome, mono right readout,
                 with the task name carried as the primary. -->
        <div class="flex items-center gap-2.5">
            {@render dot()}
            <span class="flex min-w-0 flex-1 flex-col gap-0.5">
                <span class="flex items-center gap-1.5">
                    <span class="truncate font-mono text-[13px] font-semibold text-on-surface">
                        {run.taskName}{#if suffix}<span class="text-on-surface-muted">{suffix}</span
                            >{/if}
                    </span>
                    <span class="shrink-0 text-on-surface-faint">·</span>
                    <span
                        class="shrink-0 font-mono text-[12px] font-semibold capitalize {config.color}"
                        >{dstatus}</span
                    >
                </span>
                <span
                    class="flex min-w-0 items-center gap-1.5 font-mono text-2xs text-on-surface-faint"
                    title={formatFullDateTime(startedAt)}
                >
                    <span class="truncate">{formatDateTime(startedAt)}</span>
                    <span class="shrink-0">· {formatTriggeredByLabel(run.triggeredBy)}</span>
                    {#if retry}
                        <span class="shrink-0 rounded bg-surface-sunken px-1 font-mono"
                            >{retry}</span
                        >
                    {/if}
                </span>
            </span>
            <span
                class="shrink-0 self-start pt-0.5 font-mono text-[11.5px] text-on-surface-faint tabular-nums"
                title={retry ?? undefined}
            >
                {runRowReadout(run, dstatus)}
            </span>
        </div>
    {:else}
        <!-- Task-rail variant (artifact ".run"): a single dense line,
                 time · date · outcome, with a mono exit/duration readout.
                 leading-tight matches the artifact's ~1.2 line-height so the
                 (descender-less) text optically centers instead of riding high
                 inside Tailwind's default 1.5 line box. -->
        <div class="flex items-center gap-[11px] leading-tight">
            {@render dot()}
            <span class="flex min-w-0 flex-1 items-center gap-1.5 truncate">
                <span
                    class="font-mono text-[12.5px] font-semibold tracking-tight text-on-surface tabular-nums"
                    title={formatFullDateTime(startedAt)}
                >
                    {formatTimeHM(startedAt)} · {formatDayMonth(startedAt)}
                </span>
                <span class="text-on-surface-faint">·</span>
                <span class="font-mono text-[12.5px] font-semibold capitalize {config.color}"
                    >{dstatus}</span
                >
                {#if suffix}
                    <span class="font-mono text-2xs text-on-surface-faint">{suffix}</span>
                {/if}
            </span>
            {#if usage && amounts}
                <span
                    class="flex w-6 shrink-0 cursor-help flex-col gap-0.5"
                    title="{runUsageLabel(run)}. Usual for this task: CPU {formatDuration(
                        usage.usual.cpu,
                    )} · RAM {formatBytes(usage.usual.ram)}"
                    data-testid="run-usage-bars"
                >
                    {@render bar(amounts.cpu, usage.max.cpu, usage.usual.cpu)}
                    {@render bar(amounts.ram, usage.max.ram, usage.usual.ram)}
                </span>
            {:else if usage !== undefined}
                <span class="w-6 shrink-0" aria-hidden="true"></span>
            {/if}
            <span
                class="shrink-0 font-mono text-[11.5px] text-on-surface-faint tabular-nums"
                title={retry ?? undefined}
            >
                {runRowReadout(run, dstatus)}
            </span>
        </div>
        {#if match}
            {@const hl = highlightParts(match.text, outputQuery)}
            <div
                class="mt-1.5 truncate rounded-[3px] border border-outline-faint bg-surface-sunken px-2 py-1 font-mono text-2xs text-on-surface-muted"
            >
                {hl.before}<mark class="rounded-[3px] bg-primary-soft px-0.5 text-primary-soft-text"
                    >{hl.match}</mark
                >{hl.after}
            </div>
        {/if}
    {/if}

    <div
        class="absolute inset-y-2 left-[-6px] w-[3px] rounded-[3px] {config.solidDot} {active
            ? 'opacity-100'
            : 'opacity-0'}"
        aria-hidden="true"
    ></div>
</button>

{#snippet bar(value: number, max: number, usual: number)}
    <span class="block h-[3px] overflow-hidden rounded-[1px] bg-outline-faint">
        <span
            class="block h-full {LEVEL_COLOR[usageLevel(value, usual)]}"
            style="width: {String(max > 0 ? Math.max(4, Math.round((100 * value) / max)) : 4)}%"
        ></span>
    </span>
{/snippet}

{#snippet dot()}
    <span
        class="{config.color} size-[9px] shrink-0 rounded-full bg-current ring-[3px] ring-current/20 {running
            ? 'animate-pulse'
            : ''} {dotFade}"
        aria-hidden="true"
    ></span>
{/snippet}
