<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import {
        ChevronDown,
        Download,
        Play,
        RefreshCcw,
        RotateCw,
        Square,
        Trash,
    } from "@lucide/svelte";
    import Button from "../Button.svelte";
    import Popover from "../Popover.svelte";
    import { runLogDownloadUrl } from "./run-helpers.js";

    let {
        runId,
        taskName,
        isRunning,
        canDelete,
        serviceStopped,
        serviceBusy,
        onRun,
        onRunAgain,
        onStop,
        onStopService,
        onRestartService,
        onDelete,
    }: {
        runId: string;
        taskName: string;
        isRunning: boolean;
        canDelete: boolean;
        serviceStopped: boolean;
        serviceBusy: boolean;
        onRun: (() => void) | undefined;
        onRunAgain: (() => void) | undefined;
        onStop: ((runId: string) => void) | undefined;
        onStopService: (() => void) | undefined;
        onRestartService: (() => void) | undefined;
        onDelete: ((runId: string) => void) | undefined;
    } = $props();

    // Inline-confirm popover for delete.
    let confirmDeleteOpen = $state(false);
    // Dropdown half of the Run split button (the "reuse parameters" variant).
    let runMenuOpen = $state(false);
</script>

<!-- Actions: run (split) / service lifecycle · stop · download · delete (delete
     behind an inline confirm) -->
<div class="flex flex-wrap items-center gap-2">
    {#if onStopService || onRestartService}
        <!-- Service lifecycle replaces the run control. Restart is always
             available (it cancels + respawns all instances in one call); Stop
             appears only while the service is running. Each still confirms, so
             neither fires accidentally. -->
        {#if onRestartService}
            <button
                type="button"
                onclick={() => onRestartService()}
                disabled={serviceBusy}
                title={serviceStopped
                    ? "Starts the service"
                    : "Cancels and respawns every instance"}
                class="inline-flex h-9 cursor-pointer items-center gap-1.5 rounded-[3px] border border-primary-soft-border bg-surface-raised px-3 font-mono text-sm font-medium text-primary hover:bg-primary-soft active:translate-y-px disabled:cursor-not-allowed disabled:opacity-60"
            >
                {#if serviceStopped}
                    <Play size={15} fill="currentColor" stroke="none" />
                {:else}
                    <RefreshCcw size={15} />
                {/if}
                <span class="@max-xs:hidden">{serviceStopped ? "Start" : "Restart"}</span>
            </button>
        {/if}
        {#if !serviceStopped && onStopService}
            <button
                type="button"
                onclick={() => onStopService()}
                disabled={serviceBusy}
                title="Stops the service for the rest of the daemon's lifetime"
                class="inline-flex h-9 cursor-pointer items-center gap-1.5 rounded-[3px] border border-danger-soft-border bg-surface-raised px-3 font-mono text-sm font-medium text-danger-surface hover:bg-danger-soft active:translate-y-px disabled:cursor-not-allowed disabled:opacity-60"
            >
                <Square size={15} fill="currentColor" stroke="none" />
                <span class="@max-xs:hidden">Stop</span>
            </button>
        {/if}
    {:else}
        {#if onRun}
            <!-- Split button: Run (defaults) + an arrow for the reuse-parameters
                 variant, shown only when there is one. -->
            <div
                class="inline-flex overflow-hidden rounded-[3px] border border-primary-soft-border"
            >
                <button
                    type="button"
                    onclick={() => onRun()}
                    title="Run this task now with default options"
                    class="inline-flex h-9 cursor-pointer items-center gap-1.5 bg-surface-raised px-3 font-mono text-sm font-medium text-primary hover:bg-primary-soft active:translate-y-px"
                >
                    <Play size={15} fill="currentColor" stroke="none" />
                    <span class="@max-xs:hidden">Run</span>
                </button>
                {#if onRunAgain}
                    <Popover bind:open={runMenuOpen} placement="bottom-end">
                        {#snippet trigger()}
                            <span
                                class="flex h-9 cursor-pointer items-center border-l border-primary-soft-border bg-surface-raised px-2 text-primary hover:bg-primary-soft"
                                title="More run options"
                                aria-label="More run options"
                            >
                                <ChevronDown size={15} />
                            </span>
                        {/snippet}
                        <button
                            type="button"
                            onclick={() => {
                                runMenuOpen = false;
                                onRunAgain();
                            }}
                            class="flex w-56 items-start gap-2.5 text-left"
                        >
                            <RotateCw size={15} class="mt-0.5 shrink-0 text-on-surface-muted" />
                            <span class="flex flex-col">
                                <span class="font-mono text-sm font-medium text-on-surface"
                                    >Run again</span
                                >
                                <span class="text-xs text-on-surface-muted"
                                    >Reuse this run's parameters</span
                                >
                            </span>
                        </button>
                    </Popover>
                {/if}
            </div>
        {/if}
        {#if isRunning && onStop}
            <button
                type="button"
                onclick={() => onStop(runId)}
                class="inline-flex h-9 cursor-pointer items-center gap-1.5 rounded-[3px] border border-danger-soft-border bg-surface-raised px-3 font-mono text-sm font-medium text-danger-surface hover:bg-danger-soft active:translate-y-px"
            >
                <Square size={15} fill="currentColor" stroke="none" />
                <span class="@max-xs:hidden">Stop</span>
            </button>
        {/if}
    {/if}
    <a
        href={runLogDownloadUrl(runId)}
        download="{taskName}-{runId}.log"
        class="inline-flex items-center justify-center rounded-[3px] border border-outline-faint bg-surface-raised p-2 text-on-surface-muted hover:border-outline-hover hover:bg-surface-sunken hover:text-primary"
        title="Download the full log (rotated and current parts as one file)"
        aria-label="Download log"
    >
        <Download size={15} />
    </a>
    {#if canDelete && onDelete}
        <Popover bind:open={confirmDeleteOpen} placement="bottom-end">
            {#snippet trigger()}
                <span
                    class="flex cursor-pointer items-center justify-center rounded-[3px] border border-outline-faint bg-surface-raised p-2 text-on-surface-muted hover:border-danger-soft-border hover:bg-danger-soft hover:text-danger-surface"
                    title="Delete this run"
                    aria-label="Delete run"
                >
                    <Trash size={15} />
                </span>
            {/snippet}
            <div class="w-60">
                <div class="font-mono text-sm font-semibold text-on-surface">Delete this run?</div>
                <div class="mt-1 text-xs leading-relaxed text-on-surface-muted">
                    Its captured output is removed from disk. This can't be undone.
                </div>
                <div class="mt-3 flex justify-end gap-2">
                    <Button variant="ghost" size="sm" onclick={() => (confirmDeleteOpen = false)}>
                        Cancel
                    </Button>
                    <Button
                        variant="danger"
                        size="sm"
                        onclick={() => {
                            confirmDeleteOpen = false;
                            onDelete(runId);
                        }}
                    >
                        Delete run
                    </Button>
                </div>
            </div>
        </Popover>
    {/if}
</div>
