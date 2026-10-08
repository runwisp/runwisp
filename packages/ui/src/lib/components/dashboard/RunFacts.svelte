<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { Check, Hash, RotateCw, SlidersHorizontal } from "@lucide/svelte";
    import Popover from "../Popover.svelte";
    import { CopyFeedback } from "../../utils/clipboard.svelte.js";
    import { formatBytes, formatCalendarDate, formatClockTime } from "../../utils/format.js";
    import { formatShortId } from "../../utils/id.js";
    import type { ResourceUsage, Run } from "@runwisp/common";
    import {
        formatTriggeredByLabel,
        runRetryLabel,
        runStartDelay,
        runUsageLabel,
    } from "./run-helpers.js";

    let { run, live }: { run: Run; live: ResourceUsage | undefined } = $props();

    const runIdCopy = new CopyFeedback(1200);

    let startDelay = $derived(runStartDelay(run));
    let retry = $derived(runRetryLabel(run));
    let usage = $derived(runUsageLabel(run));
    let paramEntries = $derived(run.params ? Object.entries(run.params) : []);
</script>

<!-- Facts: when it happened and what shaped it. Queue wait, retries and
     parameters are absent on an ordinary run and appear inline the moment they
     exist. -->
<div
    class="mt-2.5 flex flex-wrap items-center gap-x-2 gap-y-1 font-mono text-[11.5px] text-on-surface-faint tabular-nums"
>
    <!-- No hover-for-the-full-timestamp here: the date sits right beside the
         clock time, so a tooltip could only repeat it. The line wraps at narrow
         widths instead of dropping the date and hiding it behind a hover. -->
    <span data-testid="run-started" class="text-on-surface-muted"
        >{run.startedAt ? formatClockTime(run.startedAt) : "—"}</span
    >
    <span class="text-outline-hover" aria-hidden="true">·</span>
    <span>{formatCalendarDate(run.startedAt ?? run.createdAt)}</span>
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
                        <dt class="font-mono text-xs text-on-surface-muted">{key}</dt>
                        <dd class="font-mono text-xs font-medium break-all text-on-surface">
                            {value}
                        </dd>
                    {/each}
                </dl>
            </div>
        </Popover>
    {/if}
    {#if live}
        <span class="text-outline-hover" aria-hidden="true">·</span>
        <span
            data-testid="run-live-usage"
            class="text-on-surface-muted"
            title="Live CPU (100% = one core) and memory of the run's processes"
            >CPU {Math.round(live.cpuPercent)}% · {formatBytes(live.memoryBytes)}</span
        >
    {/if}
    {#if usage}
        <span class="text-outline-hover" aria-hidden="true">·</span>
        <span data-testid="run-usage" title="Peak memory and CPU time of the run's processes"
            >{usage}</span
        >
    {/if}
    <!-- Run-id chip: click to copy the full ULID. Last, because it is the fact
         you reach for least and the only one you interact with. -->
    <button
        type="button"
        onclick={() => void runIdCopy.copy(run.id)}
        title="Copy run ID"
        class="inline-flex items-center gap-1 rounded-[3px] border border-outline-faint bg-surface-sunken px-1.5 text-on-surface-faint hover:border-outline-hover hover:text-primary"
    >
        {#if runIdCopy.copied}
            <Check size={11} class="text-success-surface" />Copied
        {:else}
            <Hash size={11} />{run.id}
        {/if}
    </button>
</div>
