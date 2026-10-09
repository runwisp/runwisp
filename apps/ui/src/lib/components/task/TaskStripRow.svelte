<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    // The task row: state word, sentence, live usage and the task's actions, on
    // one line that never wraps. What doesn't fit folds into ⋯ in a fixed order,
    // so a button is always in one of two places. Used by the strip and, folded,
    // by the top bar.
    import { ChevronDown, Ellipsis, Pause, Play, RefreshCcw, Square } from "@lucide/svelte";
    import type { Component } from "svelte";
    import type { ResourceUsage } from "@runwisp/common";
    import { Button, Dropdown, Popover, foldToFit, formatBytes } from "@runwisp/ui";
    import { HELD_BY_CRON_HELP } from "$lib/utils/task";
    import {
        STRIP_TONES,
        type StripActionKind,
        type StripText,
        type TaskStripModel,
    } from "$lib/utils/task-strip";

    let {
        model,
        usage,
        variant,
        busy,
        detailsOpen,
        onAction,
        onOpenRun,
        onDetails,
        onUnfold,
    }: {
        model: TaskStripModel;
        usage: ResourceUsage | undefined;
        variant: "strip" | "bar";
        busy: boolean;
        detailsOpen: boolean;
        onAction: (kind: StripActionKind) => void;
        onOpenRun: (runId: string) => void;
        onDetails: () => void;
        onUnfold?: () => void;
    } = $props();

    const ICONS: Record<StripActionKind, Component> = {
        run: Play,
        pause: Pause,
        resume: Play,
        start: Play,
        restart: RefreshCcw,
        "stop-service": Square,
    };
    const bar = $derived(variant === "bar");
    // A link that does what the badge does can fold away; one that says
    // something new (how to hand a held job over) stays.
    const sameLink = $derived(!!model.link?.runId && model.link.runId === model.badge.runId);
    const usageText = $derived(
        usage
            ? `CPU ${String(Math.round(usage.cpuPercent))}% · RAM ${formatBytes(usage.memoryBytes)}`
            : undefined,
    );
    // Fold order: the duplicate link, Details, the long sentence for the short
    // one, the other actions from the right, live usage. The badge and the
    // first action always stay.
    const order = $derived([
        ...(sameLink ? ["link"] : []),
        "details",
        ...(bar ? [] : ["sentence"]),
        ...model.actions.slice(1).map((_, i) => `a${String(model.actions.length - 1 - i)}`),
        ...(usageText ? ["usage"] : []),
    ]);
    let folded = $state<string[]>([]);

    const menu = $derived.by(() => {
        const items: { label: string; onClick?: () => void; disabled?: boolean; title?: string }[] =
            [];
        const link = model.link;
        if (folded.includes("link") && link?.runId) {
            const runId = link.runId;
            items.push({ label: link.text, onClick: () => onOpenRun(runId) });
        }
        model.actions.forEach((a, i) => {
            if (folded.includes(`a${String(i)}`)) {
                items.push({ label: a.label, title: a.title, onClick: () => onAction(a.kind) });
            }
        });
        if (folded.includes("details")) items.push({ label: "Details", onClick: onDetails });
        if (folded.includes("usage") && usageText) {
            items.push({ label: `Now: ${usageText}`, disabled: true });
        }
        return items;
    });
</script>

{#snippet text(parts: StripText[])}
    {#each parts as part, i (i)}<span
            class="{part.muted ? 'text-on-surface-faint' : ''} {part.struck
                ? 'line-through'
                : ''} {part.mono ? 'font-mono text-[0.92em]' : ''}">{part.text}</span
        >{/each}
{/snippet}

{#snippet runLink(className: string, fold: boolean)}
    {#if model.link?.runId}
        {@const runId = model.link.runId}
        <button
            type="button"
            data-fold={fold ? "link" : undefined}
            class="cursor-pointer text-primary underline-offset-2 hover:underline {className}"
            onclick={() => onOpenRun(runId)}>{model.link.text}</button
        >
    {/if}
{/snippet}

<div
    use:foldToFit={{ order, onfold: (keys) => (folded = keys) }}
    class="flex min-w-0 flex-1 items-center overflow-hidden whitespace-nowrap {bar
        ? 'gap-2'
        : 'gap-2.5'}"
    data-testid={bar ? "task-bar" : "task-strip-row"}
>
    {#if model.badge.runId}
        {@const runId = model.badge.runId}
        <button
            type="button"
            class="shrink-0 cursor-pointer rounded-[3px] border font-mono font-semibold underline decoration-dotted underline-offset-3 {STRIP_TONES[
                model.badge.tone
            ]} {bar ? 'px-1.5 py-px text-xs' : 'px-2 py-0.5 text-[13px]'}"
            title={model.badge.title}
            data-testid="task-state"
            onclick={() => onOpenRun(runId)}>{model.badge.text}</button
        >
    {:else}
        <span
            class="shrink-0 rounded-[3px] border font-mono font-semibold {STRIP_TONES[
                model.badge.tone
            ]} {bar ? 'px-1.5 py-px text-xs' : 'px-2 py-0.5 text-[13px]'}"
            title={model.badge.title}
            data-testid="task-state">{model.badge.text}</span
        >
    {/if}

    <span
        data-fold-shrink
        class="min-w-0 truncate text-on-surface-muted {bar ? 'text-xs' : 'text-sm'}"
        data-testid="task-sentence"
    >
        {#if bar}
            {@render text(model.short)}
        {:else}
            <span data-fold="sentence">{@render text(model.sentence)}</span>
            <span data-fold-alt="sentence" hidden>{@render text(model.short)}</span>
        {/if}
        {#if sameLink}
            {@render runLink("ml-1", true)}
        {/if}
    </span>

    {#if model.link && !sameLink}
        {#if model.link.held}
            <Popover placement="bottom-start" class="shrink-0">
                {#snippet trigger()}
                    <span
                        class="cursor-pointer text-primary underline-offset-2 hover:underline {bar
                            ? 'text-xs'
                            : 'text-sm'}">{model.link?.text}</span
                    >
                {/snippet}
                <p class="w-80 max-w-full text-xs leading-relaxed whitespace-normal">
                    {HELD_BY_CRON_HELP}
                </p>
            </Popover>
        {:else}
            {@render runLink(`shrink-0 ${bar ? "text-xs" : "text-sm"}`, false)}
        {/if}
    {/if}

    {#if usageText}
        <span
            data-fold="usage"
            class="shrink-0 font-mono text-2xs text-on-surface-muted tabular-nums"
            title="Live CPU (100% = one core) and memory of everything this task is running"
            data-testid="task-usage">{usageText}</span
        >
    {/if}

    <div class="ml-auto flex shrink-0 items-center gap-1.5">
        {#each model.actions as action, i (action.kind + action.label)}
            {@const Icon = ICONS[action.kind]}
            <Button
                variant={action.primary ? "primary" : "secondary"}
                size={bar ? "xs" : "sm"}
                class="shrink-0"
                data-fold={i > 0 ? `a${String(i)}` : undefined}
                title={action.title}
                loading={busy && action.kind !== "run"}
                onclick={() => onAction(action.kind)}
            >
                {#snippet icon()}<Icon size={bar ? 12 : 14} />{/snippet}
                {action.label}
            </Button>
        {/each}
        <Button
            variant="secondary"
            size={bar ? "xs" : "sm"}
            class="shrink-0"
            data-fold="details"
            aria-expanded={detailsOpen}
            onclick={onDetails}>Details</Button
        >
        {#if menu.length > 0}
            <Dropdown items={menu} triggerLabel="More task actions">
                {#snippet trigger()}<Ellipsis size={bar ? 14 : 16} />{/snippet}
            </Dropdown>
        {/if}
        {#if bar && onUnfold}
            <button
                type="button"
                class="shrink-0 rounded-[3px] p-1 text-on-surface-muted hover:bg-surface-sunken hover:text-primary"
                title="Show the task header"
                aria-label="Show the task header"
                onclick={onUnfold}
            >
                <ChevronDown size={16} />
            </button>
        {/if}
    </div>
</div>
