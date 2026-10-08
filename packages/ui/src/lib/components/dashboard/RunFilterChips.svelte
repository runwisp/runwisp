<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { X } from "@lucide/svelte";
    import {
        activeDimensions,
        clearDimension,
        filterChipLabel,
        type RunsListFilters,
    } from "./run-filters.js";

    let {
        filters = $bindable(),
        showTask,
    }: {
        filters: RunsListFilters;
        /** `task` only chips on the cross-task view; elsewhere it is the page scope. */
        showTask: boolean;
    } = $props();

    const chips = $derived(
        activeDimensions(filters)
            .filter((dim) => dim !== "task" || showTask)
            .map((dim) => ({ dimension: dim, label: filterChipLabel(filters, dim) })),
    );
</script>

<!-- Rendered only when filters are set, so the header stays clean when empty;
     each chip's X clears its dimension. The filter controls themselves live in
     the popover. -->
{#if chips.length > 0}
    <div
        class="flex shrink-0 flex-wrap gap-1 border-b border-outline-faint bg-surface-sunken px-3 py-2"
    >
        {#each chips as chip (chip.dimension)}
            <span
                class="inline-flex items-center gap-1 rounded-[3px] border border-primary-soft-border bg-primary-soft py-0.5 pr-1 pl-2 font-mono text-2xs font-medium text-primary-soft-text"
            >
                {chip.label}
                <button
                    type="button"
                    onclick={() => (filters = clearDimension(filters, chip.dimension))}
                    class="flex size-3.5 items-center justify-center rounded-[3px] text-primary-soft-text/70 hover:bg-primary/15 hover:text-primary-soft-text"
                    aria-label="Remove {chip.label} filter"
                >
                    <X size={11} />
                </button>
            </span>
        {/each}
    </div>
{/if}
