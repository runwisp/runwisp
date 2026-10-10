<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { ChevronDown, Download, Play, RotateCw, Square, Trash } from "@lucide/svelte";
    import Button from "../Button.svelte";
    import Popover from "../Popover.svelte";
    import { runLogDownloadUrl } from "./run-helpers.js";

    let {
        runId,
        taskName,
        isRunning,
        canDelete,
        onRun,
        onRunAgain,
        onStop,
        onDelete,
        stopLabel = "Stop",
    }: {
        runId: string;
        taskName: string;
        isRunning: boolean;
        canDelete: boolean;
        onRun: (() => void) | undefined;
        onRunAgain: (() => void) | undefined;
        onStop: ((runId: string) => void) | undefined;
        onDelete: ((runId: string) => void) | undefined;
        stopLabel?: string | undefined;
    } = $props();

    // Inline-confirm popover for delete.
    let confirmDeleteOpen = $state(false);
    // Dropdown half of the Run split button (the "reuse parameters" variant).
    let runMenuOpen = $state(false);
</script>

<!-- Run-level actions: run (split) · stop · download · delete (delete behind an
     inline confirm). Task-level ones (pause, service start/stop/restart) live
     on the task header. -->
<div class="flex flex-wrap items-center gap-2">
    {#if onRun}
        <!-- Split button: Run (defaults) + an arrow for the reuse-parameters
                 variant, shown only when there is one. -->
        <div class="inline-flex overflow-hidden rounded-[3px] border border-primary-soft-border">
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
    {#if onRunAgain && !onRun && !isRunning}
        <!-- No Run button here (the page has its own), so re-running with this
             run's parameters stands alone. -->
        <button
            type="button"
            onclick={() => onRunAgain()}
            title="Run again, reusing this run's parameters"
            aria-label="Run again"
            class="inline-flex h-9 cursor-pointer items-center gap-1.5 rounded-[3px] border border-primary-soft-border bg-surface-raised px-3 font-mono text-sm font-medium text-primary hover:bg-primary-soft active:translate-y-px @max-md:px-2.5"
        >
            <RotateCw size={15} />
            <span class="@max-md:hidden">Run again</span>
        </button>
    {/if}
    {#if isRunning && onStop}
        <button
            type="button"
            onclick={() => onStop(runId)}
            aria-label={stopLabel}
            class="inline-flex h-9 cursor-pointer items-center gap-1.5 rounded-[3px] border border-danger-soft-border bg-surface-raised px-3 font-mono text-sm font-medium text-danger-surface hover:bg-danger-soft active:translate-y-px"
        >
            <Square size={15} fill="currentColor" stroke="none" />
            <span class="@max-xs:hidden">{stopLabel}</span>
        </button>
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
