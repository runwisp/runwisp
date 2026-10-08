<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { CalendarClock, Pause, Play } from "@lucide/svelte";
    import {
        Button,
        Popover,
        extractErrorMessage,
        formatRelativeTimeWithAbsolute,
        humanizeCron,
        toast,
    } from "@runwisp/ui";
    import type { Task } from "@runwisp/common";
    import { tasksApi } from "$lib/api";
    import { systemStore, taskStore } from "$lib/stores";
    import { canTogglePause } from "$lib/utils/task";
    import { UNDO_MS } from "$lib/utils/run-actions";

    let { task }: { task: Task } = $props();

    let open = $state(false);
    let busy = $state(false);

    const schedule = $derived(humanizeCron(task.cron ?? ""));
    const timezone = $derived(task.timezone ?? systemStore.timezone);
    const togglable = $derived(canTogglePause(task));

    // Icon-only on phones, and beside the page search while the top bar (the
    // nearest @container) is too narrow for the name, label and search at once.
    const labelClass = "max-sm:sr-only md:@max-4xl:sr-only";

    // The task list is only refetched on changes, so "Next run" could be one
    // tick old by the time the popover opens.
    $effect(() => {
        if (open) void taskStore.refresh();
    });

    async function setPaused(name: string, pause: boolean): Promise<void> {
        await (pause ? tasksApi.pauseSchedule(name) : tasksApi.resumeSchedule(name));
        await taskStore.refresh();
    }

    async function toggle(): Promise<void> {
        const name = task.name;
        const pause = !task.pausedAt;
        busy = true;
        try {
            await setPaused(name, pause);
            open = false;
            toast.success(pause ? `Paused the schedule of "${name}"` : `Resumed "${name}"`, {
                duration: UNDO_MS,
                action: {
                    label: "Undo",
                    onClick: () => {
                        setPaused(name, !pause).catch((err: unknown) => {
                            toast.error(extractErrorMessage(err, "Failed to undo"));
                        });
                    },
                },
            });
        } catch (err) {
            toast.error(
                extractErrorMessage(
                    err,
                    pause ? "Failed to pause the schedule" : "Failed to resume the schedule",
                ),
            );
        } finally {
            busy = false;
        }
    }
</script>

<Popover bind:open placement="bottom-start" mobileSheet class="shrink-0">
    {#snippet trigger()}
        <span
            class="flex cursor-pointer items-center gap-1.5 rounded-[3px] border px-2 py-0.5 font-mono text-xs {task.pausedAt
                ? 'border-warning-soft-border bg-warning-soft text-warning-soft-text'
                : 'border-outline text-on-surface-muted hover:border-outline-hover hover:text-primary'}"
            title={task.pausedAt ? "Cron schedule paused" : `Cron schedule: ${schedule.raw}`}
            data-testid="schedule-chip"
        >
            {#if task.pausedAt}
                <Pause size={12} class="shrink-0" />
                <span class={labelClass}>Schedule paused</span>
            {:else}
                <CalendarClock size={12} class="shrink-0" />
                <span class="max-w-[16rem] truncate {labelClass}">{schedule.humanized}</span>
            {/if}
        </span>
    {/snippet}

    <div class="flex w-72 max-w-full flex-col gap-3 font-sans text-sm">
        <div class="flex flex-col gap-0.5">
            <span class="font-medium text-on-surface">{schedule.humanized}</span>
            <span class="font-mono text-xs text-on-surface-muted">
                {schedule.raw}{#if timezone}&nbsp;· {timezone}{/if}
            </span>
        </div>

        <p class="text-xs text-on-surface-muted">
            {#if task.pausedAt}
                <span class="font-medium text-warning-soft-text">Paused</span>
                {formatRelativeTimeWithAbsolute(task.pausedAt)}.
            {:else if task.nextRunAt}
                Next run {formatRelativeTimeWithAbsolute(task.nextRunAt)}.
            {:else}
                No upcoming run.
            {/if}
        </p>

        {#if togglable}
            <Button
                variant={task.pausedAt ? "primary" : "secondary"}
                size="sm"
                loading={busy}
                onclick={() => void toggle()}
            >
                {#if task.pausedAt}
                    <Play size={14} /> Resume schedule
                {:else}
                    <Pause size={14} /> Pause schedule
                {/if}
            </Button>
            <p class="text-xs text-on-surface-faint">
                Skipped ticks are not caught up. Manual runs still work.
            </p>
        {:else if !task.manualTrigger}
            <p class="text-xs text-on-surface-faint">
                Locked by <span class="font-mono">manual_trigger = false</span> in runwisp.toml.
            </p>
        {:else}
            <p class="text-xs text-on-surface-faint">
                A system cron daemon still runs this task, so its schedule can't be paused here.
            </p>
        {/if}
    </div>
</Popover>
