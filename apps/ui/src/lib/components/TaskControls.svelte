<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    // Task-level actions on the top bar, visible without opening a run: Pause
    // or Resume a cron schedule, and Start, Restart or Stop a service.
    // Run-level actions (stop this run, delete) stay in the run panel.
    import { Pause, Play, RefreshCcw, Square } from "@lucide/svelte";
    import { isService, type Task } from "@runwisp/common";
    import { AlertDialog, Button, extractErrorMessage, toast } from "@runwisp/ui";
    import { tasksApi } from "$lib/api";
    import { systemStore, taskStore } from "$lib/stores";
    import {
        canTogglePause,
        isServiceStopped,
        showScheduleChip,
        taskInstanceCount,
    } from "$lib/utils/task";
    import { UNDO_MS } from "$lib/utils/run-actions";

    let { task }: { task: Task } = $props();

    let busy = $state(false);
    let restartConfirmOpen = $state(false);
    let stopConfirmOpen = $state(false);

    const pausable = $derived(
        showScheduleChip(task, systemStore.schedulingActive) && canTogglePause(task),
    );
    // manual_trigger on a service gates stop/restart/start; false locks it to
    // its restart policy until a runwisp.toml edit + reload.
    const controllable = $derived(isService(task.kind) && task.manualTrigger);
    const stopped = $derived(isServiceStopped(task));
    const instanceCount = $derived(taskInstanceCount(task));

    // Icon-only on phones and while the top bar is too narrow, like the chip.
    const labelClass = "max-sm:sr-only md:@max-4xl:sr-only";

    async function setPaused(name: string, pause: boolean): Promise<void> {
        await (pause ? tasksApi.pauseSchedule(name) : tasksApi.resumeSchedule(name));
        await taskStore.refresh();
    }

    async function togglePause(): Promise<void> {
        const name = task.name;
        const pause = !task.pausedAt;
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
        start: { call: tasksApi.restartService, done: "Starting", failed: "start" },
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
</script>

{#if pausable}
    <Button
        variant="secondary"
        size="xs"
        class="shrink-0"
        loading={busy}
        title={task.pausedAt
            ? "Resume the cron schedule"
            : "Pause the cron schedule. Manual runs still work."}
        aria-label={task.pausedAt ? "Resume schedule" : "Pause schedule"}
        onclick={() => void togglePause()}
    >
        {#snippet icon()}
            {#if task.pausedAt}<Play size={12} />{:else}<Pause size={12} />{/if}
        {/snippet}
        <span class={labelClass}>{task.pausedAt ? "Resume" : "Pause"}</span>
    </Button>
{/if}

{#if controllable}
    <Button
        variant="secondary"
        size="xs"
        class="shrink-0"
        loading={busy}
        title={stopped ? "Start the service" : "Cancel and respawn every instance"}
        aria-label={stopped ? "Start service" : "Restart service"}
        onclick={() => (restartConfirmOpen = true)}
    >
        {#snippet icon()}
            {#if stopped}<Play size={12} />{:else}<RefreshCcw size={12} />{/if}
        {/snippet}
        <span class={labelClass}>{stopped ? "Start" : "Restart"}</span>
    </Button>
    {#if !stopped}
        <Button
            variant="secondary"
            size="xs"
            class="shrink-0"
            disabled={busy}
            title="Stop the service until you start it again or the daemon restarts"
            aria-label="Stop service"
            onclick={() => (stopConfirmOpen = true)}
        >
            {#snippet icon()}<Square size={12} />{/snippet}
            <span class={labelClass}>Stop</span>
        </Button>
    {/if}

    <AlertDialog
        bind:open={restartConfirmOpen}
        title={stopped ? "Start Service" : "Restart Service"}
        description={stopped
            ? `Start ${task.name}?`
            : instanceCount > 1
              ? `Cancel and restart all ${String(instanceCount)} instances of ${task.name}?`
              : `Cancel and restart ${task.name}?`}
        confirmLabel={stopped ? "Start Now" : "Restart Now"}
        confirmVariant="primary"
        confirmIcon={stopped ? Play : RefreshCcw}
        onConfirm={() => serviceAction(stopped ? "start" : "restart")}
    />

    <AlertDialog
        bind:open={stopConfirmOpen}
        title="Stop Service"
        description={`Stop ${task.name}? The daemon will not restart it until you click Start or the daemon itself restarts.`}
        confirmLabel="Stop Now"
        confirmVariant="danger"
        confirmIcon={Square}
        onConfirm={() => serviceAction("stop")}
    />
{/if}
