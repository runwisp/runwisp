<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    // The task strip: what the task is doing, what happens next, and every
    // action on the task, above its runs. Folded, its row moves into the top
    // bar (desktop) or shrinks to one line (phone) to give the log the room.
    import { ChevronDown, Play, RefreshCcw, Square } from "@lucide/svelte";
    import { slide } from "svelte/transition";
    import type { Run, Task } from "@runwisp/common";
    import {
        AlertDialog,
        Button,
        TickingNow,
        extractErrorMessage,
        formatBytes,
        toast,
    } from "@runwisp/ui";
    import { tasksApi } from "$lib/api";
    import { systemStore, taskBarStore, taskStore } from "$lib/stores";
    import { FOLD_MS, type HeaderFold } from "$lib/utils/header-fold.svelte";
    import { UNDO_MS } from "$lib/utils/run-actions";
    import { taskInstanceCount } from "$lib/utils/task";
    import { STRIP_TONES, taskStrip, type StripActionKind } from "$lib/utils/task-strip";
    import TaskStripRow from "./TaskStripRow.svelte";

    let {
        task,
        running,
        fold,
        phone,
        detailsOpen,
        onDetails,
        onRun,
        onOpenRun,
    }: {
        task: Task;
        /** The newest running run of this task, if any. */
        running: Run | undefined;
        fold: HeaderFold;
        phone: boolean;
        detailsOpen: boolean;
        onDetails: () => void;
        /** Open the Run dialog. */
        onRun: () => void;
        onOpenRun: (runId: string) => void;
    } = $props();

    // Live durations ("Running · 1m 03s", "next run in …") tick on their own.
    const clock = new TickingNow(1000);
    $effect(() => clock.start());

    const model = $derived(
        taskStrip({
            task,
            running,
            schedulingActive: systemStore.schedulingActive,
            now: clock.now,
        }),
    );
    const usage = $derived(systemStore.usageFor(task));
    const primary = $derived(model.actions.find((a) => a.primary));
    const motion = $derived(
        typeof matchMedia === "function" && matchMedia("(prefers-reduced-motion: reduce)").matches
            ? 0
            : FOLD_MS,
    );

    let busy = $state(false);
    let restartConfirmOpen = $state(false);
    let stopConfirmOpen = $state(false);
    let startConfirmOpen = $state(false);

    async function setPaused(name: string, pause: boolean): Promise<void> {
        await (pause ? tasksApi.pauseSchedule(name) : tasksApi.resumeSchedule(name));
        await taskStore.refresh();
    }

    async function togglePause(pause: boolean): Promise<void> {
        const name = task.name;
        busy = true;
        try {
            await setPaused(name, pause);
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

    const SERVICE_ACTIONS = {
        restart: { call: tasksApi.restartService, done: "Restarting", failed: "restart" },
        start: { call: tasksApi.startService, done: "Starting", failed: "start" },
        stop: { call: tasksApi.stopService, done: "Stopped", failed: "stop" },
    };

    async function serviceAction(action: keyof typeof SERVICE_ACTIONS): Promise<void> {
        const name = task.name;
        const { call, done, failed } = SERVICE_ACTIONS[action];
        busy = true;
        try {
            await call(name);
            void taskStore.refresh();
            toast.success(`${done} "${name}"`);
        } catch (err) {
            toast.error(extractErrorMessage(err, `Failed to ${failed} "${name}"`));
        } finally {
            busy = false;
        }
    }

    function act(kind: StripActionKind): void {
        switch (kind) {
            case "run":
                onRun();
                return;
            case "pause":
            case "resume":
                void togglePause(kind === "pause");
                return;
            case "start":
                startConfirmOpen = true;
                return;
            case "restart":
                restartConfirmOpen = true;
                return;
            case "stop-service":
                stopConfirmOpen = true;
        }
    }

    // The description gets one line (two on a phone); "more" shows only when
    // it was actually cut.
    let descOpen = $state(false);
    let descEl = $state<HTMLElement | null>(null);
    let descClamped = $state(false);
    $effect(() => {
        const el = descEl;
        if (!el) return;
        const measure = () => {
            descClamped = el.scrollHeight > el.clientHeight + 1;
        };
        measure();
        const ro = new ResizeObserver(measure);
        ro.observe(el);
        return () => ro.disconnect();
    });

    // How much the fold gives the log: the strip's full height on desktop
    // (its row moves into the top bar), the difference to one line on a phone.
    let fullHeight = $state(0);
    let foldedHeight = $state(0);
    $effect(() => {
        fold.stripDelta = phone ? Math.max(0, fullHeight - foldedHeight) : fullHeight;
    });

    // Folded on desktop, the top bar shows the row.
    $effect(() => {
        if (fold.folded && !phone) taskBarStore.show(barRow);
        else taskBarStore.hide();
    });
    $effect(() => () => taskBarStore.hide());

    const instanceCount = $derived(taskInstanceCount(task));
</script>

{#snippet barRow()}
    <TaskStripRow
        {model}
        {usage}
        variant="bar"
        {busy}
        {detailsOpen}
        onAction={act}
        {onOpenRun}
        {onDetails}
        onUnfold={() => fold.set(false)}
    />
{/snippet}

{#if phone}
    {#if fold.folded}
        <section
            class="flex shrink-0 items-center gap-2 border-b border-outline bg-surface-raised px-2.5 py-1.5"
            data-testid="task-strip"
            data-folded
            bind:clientHeight={foldedHeight}
        >
            {@render badge("px-1.5 py-px text-xs")}
            <span class="min-w-0 flex-1 truncate text-xs text-on-surface-muted">
                {model.short.map((p) => p.text).join("")}
            </span>
            {#if primary}
                <Button
                    variant="primary"
                    size="xs"
                    class="shrink-0"
                    onclick={() => act(primary.kind)}>{primary.label}</Button
                >
            {/if}
            <button
                type="button"
                class="shrink-0 rounded-[3px] p-1 text-on-surface-muted hover:text-primary"
                aria-label="Show the task header"
                onclick={() => fold.set(false)}
            >
                <ChevronDown size={16} />
            </button>
        </section>
    {:else}
        <!-- A phone has the height, not the width: the sentence wraps and the
             actions get their own row. -->
        <section
            class="shrink-0 border-b border-outline bg-surface-raised px-3.5 py-3"
            data-testid="task-strip"
            bind:clientHeight={fullHeight}
        >
            <p class="text-sm leading-relaxed text-on-surface-muted" data-testid="task-sentence">
                {@render badge("mr-1.5 px-2 py-0.5 text-[13px]")}
                {model.sentence.map((p) => p.text).join("")}
                {#if model.link?.runId}
                    {@const runId = model.link.runId}
                    <button
                        type="button"
                        class="text-primary underline underline-offset-2"
                        onclick={() => onOpenRun(runId)}>{model.link.text}</button
                    >
                {/if}
                {#if usage}
                    <span
                        class="font-mono text-2xs whitespace-nowrap tabular-nums"
                        data-testid="task-usage"
                        >CPU {Math.round(usage.cpuPercent)}% · RAM {formatBytes(
                            usage.memoryBytes,
                        )}</span
                    >
                {/if}
            </p>
            {@render description("line-clamp-2")}
            <div class="mt-2.5 flex flex-wrap gap-2">
                {#each model.actions as action (action.kind + action.label)}
                    <Button
                        variant={action.primary ? "primary" : "secondary"}
                        size="sm"
                        class="flex-1 justify-center"
                        title={action.title}
                        loading={busy && action.kind !== "run"}
                        onclick={() => act(action.kind)}>{action.label}</Button
                    >
                {/each}
                <Button variant="secondary" size="sm" onclick={onDetails}>Details</Button>
            </div>
        </section>
    {/if}
{:else if !fold.folded}
    <section
        transition:slide={{ duration: motion }}
        class="shrink-0 border-b border-outline bg-surface-raised px-6 py-2.5"
        data-testid="task-strip"
        bind:clientHeight={fullHeight}
    >
        <div class="flex min-w-0 items-center">
            <TaskStripRow
                {model}
                {usage}
                variant="strip"
                {busy}
                {detailsOpen}
                onAction={act}
                {onOpenRun}
                {onDetails}
            />
        </div>
        {@render description("line-clamp-1")}
    </section>
{/if}

{#snippet badge(size: string)}
    {#if model.badge.runId}
        {@const runId = model.badge.runId}
        <button
            type="button"
            class="inline-block shrink-0 rounded-[3px] border font-mono font-semibold underline decoration-dotted underline-offset-3 {STRIP_TONES[
                model.badge.tone
            ]} {size}"
            title={model.badge.title}
            data-testid="task-state"
            onclick={() => onOpenRun(runId)}>{model.badge.text}</button
        >
    {:else}
        <span
            class="inline-block shrink-0 rounded-[3px] border font-mono font-semibold {STRIP_TONES[
                model.badge.tone
            ]} {size}"
            title={model.badge.title}
            data-testid="task-state">{model.badge.text}</span
        >
    {/if}
{/snippet}

{#snippet description(clamp: string)}
    {#if task.description}
        <div class="mt-1.5 flex items-end gap-2 text-sm text-on-surface-muted">
            <p
                bind:this={descEl}
                class="min-w-0 flex-1 {descOpen ? '' : clamp}"
                data-testid="task-description"
            >
                {task.description}
            </p>
            {#if descClamped || descOpen}
                <button
                    type="button"
                    class="shrink-0 text-sm text-on-surface underline underline-offset-2 hover:text-primary"
                    onclick={() => (descOpen = !descOpen)}>{descOpen ? "less" : "more"}</button
                >
            {/if}
        </div>
    {/if}
{/snippet}

<AlertDialog
    bind:open={startConfirmOpen}
    title="Start Service"
    description={`Start the instances of ${task.name} that aren't running? Running ones are left alone.`}
    confirmLabel="Start Now"
    confirmVariant="primary"
    confirmIcon={Play}
    onConfirm={() => serviceAction("start")}
/>

<AlertDialog
    bind:open={restartConfirmOpen}
    title="Restart Service"
    description={instanceCount > 1
        ? `Cancel and restart all ${String(instanceCount)} instances of ${task.name}, healthy ones included?`
        : `Cancel and restart ${task.name}?`}
    confirmLabel="Restart Now"
    confirmVariant="primary"
    confirmIcon={RefreshCcw}
    onConfirm={() => serviceAction("restart")}
/>

<AlertDialog
    bind:open={stopConfirmOpen}
    title="Stop Service"
    description={task.autostart
        ? `Stop ${task.name}? It stays down until you start it or the daemon restarts.`
        : `Stop ${task.name}? It stays down until you start it.`}
    confirmLabel="Stop Now"
    confirmVariant="danger"
    confirmIcon={Square}
    onConfirm={() => serviceAction("stop")}
/>
