<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";
    import {
        ArrowLeft,
        Check,
        Copy,
        Ellipsis,
        PanelLeftClose,
        PanelLeftOpen,
        SlidersHorizontal,
    } from "@lucide/svelte";
    import Popover from "../Popover.svelte";
    import { foldToFit } from "../../actions/fold-to-fit.js";
    import { CopyFeedback } from "../../utils/clipboard.svelte.js";
    import {
        formatBytes,
        formatClockTime,
        formatDayMonth,
        formatFullDateTime,
    } from "../../utils/format.js";
    import { formatShortId } from "../../utils/id.js";
    import type { ResourceUsage, Run, RunStatus } from "@runwisp/common";
    import { RUN_STATUS_CONFIG } from "./status-config.js";
    import {
        formatTriggeredByLabel,
        runRetryLabel,
        runStartDelay,
        runUsageLabel,
        RUN_VERDICTS,
    } from "./run-helpers.js";

    let {
        run,
        status,
        duration,
        suffix,
        live,
        onBack,
        onToggleList,
        listVisible,
        actions,
    }: {
        run: Run;
        status: RunStatus;
        duration: string | undefined;
        suffix: string;
        live: ResourceUsage | undefined;
        onBack: (() => void) | undefined;
        onToggleList: (() => void) | undefined;
        listVisible: boolean;
        actions: Snippet;
    } = $props();

    // The facts after the verdict, in reading order. `fold` is the order they
    // give way when the line runs out of room: the ID you reach for least goes
    // first, what the run used goes last.
    type FactKey = "start" | "queued" | "via" | "retry" | "params" | "instance" | "usage" | "id";
    const FOLD_ORDER: FactKey[] = [
        "id",
        "via",
        "queued",
        "start",
        "params",
        "retry",
        "instance",
        "usage",
    ];
    interface Fact {
        key: FactKey;
        label: string;
        value: string;
    }

    const runIdCopy = new CopyFeedback(1200);

    let config = $derived(RUN_STATUS_CONFIG[status]);
    let verdict = $derived(RUN_VERDICTS[status]);
    let paramEntries = $derived(run.params ? Object.entries(run.params) : []);
    let usage = $derived(
        live
            ? `CPU ${String(Math.round(live.cpuPercent))}% · RAM ${formatBytes(live.memoryBytes)}`
            : runUsageLabel(run),
    );
    let facts = $derived.by(() => {
        const list: Fact[] = [];
        const started = run.startedAt;
        if (started) {
            list.push({
                key: "start",
                label: "Started",
                value: `${formatDayMonth(started)} ${formatClockTime(started)}`,
            });
        }
        const delay = runStartDelay(run);
        if (delay) list.push({ key: "queued", label: "Queued", value: `${delay} queued` });
        list.push({
            key: "via",
            label: "Triggered",
            value: `via ${formatTriggeredByLabel(run.triggeredBy).toLowerCase()}`,
        });
        if (runRetryLabel(run)) {
            const of = run.retryOfRunId ? ` of ${formatShortId(run.retryOfRunId)}` : "";
            list.push({
                key: "retry",
                label: "Retry",
                value: `retry #${String(run.retryAttempt)}${of}`,
            });
        }
        if (paramEntries.length > 0) {
            const n = paramEntries.length;
            list.push({
                key: "params",
                label: "Parameters",
                value: `${String(n)} ${n === 1 ? "parameter" : "parameters"}`,
            });
        }
        if (suffix) list.push({ key: "instance", label: "Instance", value: `instance ${suffix}` });
        if (usage) list.push({ key: "usage", label: live ? "Now" : "Used", value: usage });
        list.push({ key: "id", label: "Run ID", value: `#${formatShortId(run.id)}` });
        return list;
    });
    let order = $derived(FOLD_ORDER.filter((key) => facts.some((f) => f.key === key)));

    let folded = $state<string[]>([]);
    let foldedFacts = $derived(facts.filter((f) => folded.includes(f.key)));

    const FACT = "before:mx-1.5 before:text-outline-hover before:content-['·']";
</script>

{#snippet paramList()}
    <dl class="grid max-h-72 grid-cols-[max-content_1fr] gap-x-4 gap-y-1.5 overflow-y-auto">
        {#each paramEntries as [key, value] (key)}
            <dt class="font-mono text-xs text-on-surface-muted">{key}</dt>
            <dd class="font-mono text-xs font-medium break-all text-on-surface">{value}</dd>
        {/each}
    </dl>
{/snippet}

{#snippet copyId()}
    <button
        type="button"
        onclick={() => void runIdCopy.copy(run.id)}
        class="inline-flex items-center gap-1 text-on-surface-muted hover:text-primary"
        title="Copy run ID"
    >
        {#if runIdCopy.copied}
            <Check size={11} class="text-success-surface" />Copied
        {:else}
            <Copy size={11} />copy
        {/if}
    </button>
{/snippet}

<!-- One line: verdict, then the facts that fit, then the actions. Facts that
     don't fit fold into ⋯, the ID first. -->
<div
    class="head-line @container relative flex shrink-0 items-center gap-2.5 border-b border-outline-faint py-[7px] pr-3.5 pl-[18px]"
    style="--rw-oc: {config.alarm ?? 'var(--color-surface-raised)'}"
>
    {#if onBack}
        <button
            type="button"
            onclick={() => onBack()}
            class="-ml-1.5 shrink-0 rounded-[3px] p-1.5 text-on-surface-muted hover:bg-surface-sunken hover:text-primary"
            title="Back to runs"
            aria-label="Back to runs"
        >
            <ArrowLeft size={18} />
        </button>
    {:else if onToggleList}
        <button
            type="button"
            onclick={() => onToggleList()}
            class="-ml-1.5 shrink-0 rounded-[3px] p-1.5 text-on-surface-muted hover:bg-surface-sunken hover:text-primary"
            title={listVisible ? "Hide run list" : "Show run list"}
            aria-label={listVisible ? "Hide run list" : "Show run list"}
            aria-expanded={listVisible}
        >
            {#if listVisible}
                <PanelLeftClose size={18} />
            {:else}
                <PanelLeftOpen size={18} />
            {/if}
        </button>
    {/if}

    <div
        use:foldToFit={{ order, onfold: (keys) => (folded = keys) }}
        class="flex min-w-0 flex-1 items-baseline gap-2.5 overflow-hidden whitespace-nowrap"
    >
        <!-- A plain title, not Tooltip: the line clips anything that overflows it. -->
        <h2
            class="flex shrink-0 items-baseline gap-x-1.5 text-[15px]"
            data-testid="run-verdict"
            data-status={status}
            data-run={run.id}
            title={config.description}
        >
            <config.icon
                size={14}
                strokeWidth={2.5}
                class="shrink-0 self-center {config.color} {status === 'running'
                    ? 'animate-spin'
                    : ''}"
            />
            <span class="font-sans font-semibold {config.alarm ? config.color : 'text-on-surface'}"
                >{verdict.verb}</span
            >
            {#if verdict.timed && duration}
                <span
                    class="font-mono text-[13px] font-medium text-on-surface tabular-nums"
                    data-testid="run-duration">{duration}</span
                >
            {/if}
            {#if status === "failed"}
                <span class="font-mono text-[13px] font-medium tabular-nums">
                    <span class="text-on-surface-faint" aria-hidden="true">·</span>
                    <span class="text-danger-surface" data-testid="run-exit"
                        >exit {run.exitCode}</span
                    >
                </span>
            {/if}
        </h2>

        <span
            data-fold-shrink
            class="min-w-0 truncate font-mono text-[11.5px] text-on-surface-faint tabular-nums"
        >
            {#each facts as fact (fact.key)}
                {#if fact.key === "start" && run.startedAt}
                    <span
                        data-fold="start"
                        data-testid="run-started"
                        class={FACT}
                        title={formatFullDateTime(run.startedAt)}>{fact.value}</span
                    >
                {:else if fact.key === "params"}
                    <span data-fold="params" class={FACT}>
                        <Popover placement="bottom-start">
                            {#snippet trigger()}
                                <span
                                    class="inline-flex cursor-pointer items-center gap-1 border-b border-dotted border-outline-hover text-on-surface-muted hover:text-primary"
                                >
                                    <SlidersHorizontal size={11} />{fact.value}
                                </span>
                            {/snippet}
                            <div class="max-w-md min-w-48">{@render paramList()}</div>
                        </Popover>
                    </span>
                {:else if fact.key === "usage"}
                    <span
                        data-fold="usage"
                        data-testid={live ? "run-live-usage" : "run-usage"}
                        class="{FACT} text-on-surface-muted"
                        title={live
                            ? "Live CPU (100% = one core) and memory of the run's processes"
                            : "CPU time and most memory the run's processes used"}
                        >{fact.value}</span
                    >
                {:else if fact.key === "id"}
                    <button
                        type="button"
                        data-fold="id"
                        onclick={() => void runIdCopy.copy(run.id)}
                        class="{FACT} hover:text-primary"
                        title="Copy run ID {run.id}"
                        >{runIdCopy.copied ? "Copied" : fact.value}</button
                    >
                {:else}
                    <span
                        data-fold={fact.key}
                        class="{FACT} {fact.key === 'queued' ? 'text-warning-soft-text' : ''}"
                        >{fact.value}</span
                    >
                {/if}
            {/each}
        </span>
    </div>

    <div class="flex shrink-0 items-center gap-2">
        {@render actions()}
        {#if foldedFacts.length > 0}
            <Popover placement="bottom-end">
                {#snippet trigger()}
                    <span
                        class="flex cursor-pointer items-center justify-center rounded-[3px] border border-outline-faint bg-surface-raised p-2 text-on-surface-muted hover:border-outline-hover hover:bg-surface-sunken hover:text-primary"
                        title="More about this run"
                        aria-label="More about this run"
                    >
                        <Ellipsis size={15} />
                    </span>
                {/snippet}
                <dl class="grid min-w-56 grid-cols-[max-content_1fr] gap-x-4 gap-y-2 text-xs">
                    {#each foldedFacts as fact (fact.key)}
                        <dt class="text-on-surface-muted">{fact.label}</dt>
                        <dd class="font-mono text-on-surface">
                            {#if fact.key === "id"}
                                <span class="mr-2 break-all">{run.id}</span>{@render copyId()}
                            {:else if fact.key === "params"}
                                {@render paramList()}
                            {:else if fact.key === "start" && run.startedAt}
                                {formatFullDateTime(run.startedAt)}
                            {:else}
                                {fact.value}
                            {/if}
                        </dd>
                    {/each}
                </dl>
            </Popover>
        {/if}
    </div>
</div>

<style>
    /* Same wash as the stacked header: only a run worth triaging tints it. */
    .head-line {
        background: color-mix(in srgb, var(--rw-oc) 7%, var(--color-surface-raised));
    }
</style>
