<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    // The strip's state word, with an icon of its own and a colour by meaning
    // so a state reads at a glance. With a run behind it, it opens that run.
    import {
        Activity,
        CalendarClock,
        CircleAlert,
        CircleStop,
        CircleX,
        Hand,
        LoaderCircle,
        Lock,
        Pause,
        RadioTower,
    } from "@lucide/svelte";
    import type { Component } from "svelte";
    import type { StripBadgeKind, TaskStripModel } from "$lib/utils/task-strip";

    let {
        badge,
        size,
        onOpenRun,
        class: className = "",
    }: {
        badge: TaskStripModel["badge"];
        size: "sm" | "md";
        onOpenRun: (runId: string) => void;
        class?: string;
    } = $props();

    // The palette has five distinct hues (info is the brand teal too), so the
    // state words share them by meaning: teal is busy right now, green is
    // healthy, amber is held back on purpose, red needs a look.
    const NEUTRAL = "border-outline bg-surface-sunken text-on-surface-muted";
    const BUSY = "border-primary-soft-border bg-primary-soft text-primary-soft-text";
    const HEALTHY = "border-success-soft-border bg-success-soft text-success-soft-text";
    const HELD_BACK = "border-warning-soft-border bg-warning-soft text-warning-soft-text";
    const NEEDS_LOOK = "border-danger-soft-border bg-danger-soft text-danger-soft-text";

    // Full class literals so Tailwind's scanner sees them.
    const LOOKS: Record<StripBadgeKind, { tone: string; icon: Component; spin?: true }> = {
        scheduled: { tone: HEALTHY, icon: CalendarClock },
        up: { tone: HEALTHY, icon: Activity },
        running: { tone: BUSY, icon: LoaderCircle, spin: true },
        starting: { tone: BUSY, icon: LoaderCircle, spin: true },
        failed: { tone: NEEDS_LOOK, icon: CircleX },
        down: { tone: NEEDS_LOOK, icon: CircleAlert },
        paused: { tone: HELD_BACK, icon: Pause },
        held: { tone: HELD_BACK, icon: Lock },
        stopped: { tone: HELD_BACK, icon: CircleStop },
        manual: { tone: NEUTRAL, icon: Hand },
        station: { tone: NEUTRAL, icon: RadioTower },
    };

    const look = $derived(LOOKS[badge.kind]);
    const Icon = $derived(look.icon);
    const sizing = $derived(
        size === "sm" ? "gap-1 px-1.5 py-px text-xs" : "gap-1.5 px-2 py-0.5 text-[13px]",
    );
    const classes = $derived(
        `inline-flex shrink-0 items-center rounded-[3px] border font-mono font-semibold whitespace-nowrap ${look.tone} ${sizing} ${className}`,
    );
</script>

{#snippet content()}
    <Icon
        size={size === "sm" ? 12 : 13}
        class="shrink-0 {look.spin ? 'motion-safe:animate-spin' : ''}"
        aria-hidden="true"
    />
    {badge.text}
{/snippet}

{#if badge.runId}
    {@const runId = badge.runId}
    <button
        type="button"
        class="{classes} cursor-pointer underline decoration-dotted underline-offset-3"
        title={badge.title}
        data-testid="task-state"
        onclick={() => onOpenRun(runId)}>{@render content()}</button
    >
{:else}
    <span class={classes} title={badge.title} data-testid="task-state">{@render content()}</span>
{/if}
