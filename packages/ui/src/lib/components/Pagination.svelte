<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import Button from "./Button.svelte";
    import { ChevronDown } from "@lucide/svelte";

    interface Props {
        page: number;
        pageSize: number;
        totalItems: number;
        pageSizeOptions?: number[] | undefined;
        onPageChange?: ((page: number) => void) | undefined;
        onPageSizeChange?: ((pageSize: number) => void) | undefined;
        class?: string | undefined;
    }

    let {
        page,
        pageSize,
        totalItems,
        pageSizeOptions = [10, 20, 50, 100],
        onPageChange,
        onPageSizeChange,
        class: className = "",
    }: Props = $props();

    const totalPages = $derived(Math.max(1, Math.ceil(totalItems / Math.max(1, pageSize))));
    const safePage = $derived(Math.min(Math.max(1, page), totalPages));

    const start = $derived(totalItems === 0 ? 0 : (safePage - 1) * pageSize + 1);
    const end = $derived(totalItems === 0 ? 0 : Math.min(totalItems, safePage * pageSize));

    function goTo(nextPage: number) {
        const clamped = Math.min(Math.max(1, nextPage), totalPages);
        onPageChange?.(clamped);
    }

    function handlePageSizeChange(e: Event & { currentTarget: EventTarget & HTMLSelectElement }) {
        const newSize = Number(e.currentTarget.value);
        onPageSizeChange?.(newSize);
    }
</script>

<div class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between {className}">
    <div class="flex items-center gap-4 font-mono text-sm text-on-surface-muted">
        {#if totalItems === 0}
            <span>No results</span>
        {:else}
            <span>Showing {start}–{end} of {totalItems}</span>
        {/if}

        <div class="flex items-center gap-2">
            <span class="text-on-surface-muted">Rows per page</span>
            <div class="relative">
                <select
                    value={pageSize}
                    onchange={handlePageSizeChange}
                    class="
                        appearance-none rounded-[3px] border border-outline bg-surface-raised py-1 pr-7 pl-2 font-mono
                        text-sm text-on-surface-muted shadow-sm hover:border-outline-hover focus:border-ring focus:ring-1 focus:ring-ring focus:outline-none
                    "
                >
                    {#each pageSizeOptions as size (size)}
                        <option value={size}>{size}</option>
                    {/each}
                </select>
                <div
                    class="pointer-events-none absolute top-1/2 right-2 -translate-y-1/2 text-on-surface-faint"
                >
                    <ChevronDown size={14} />
                </div>
            </div>
        </div>
    </div>

    <div class="flex items-center gap-2">
        <Button
            variant="secondary"
            size="xs"
            disabled={safePage <= 1}
            onclick={() => goTo(safePage - 1)}>Prev</Button
        >
        <div class="min-w-[4rem] text-center font-mono text-xs text-on-surface-muted">
            Page {safePage} of {totalPages}
        </div>
        <Button
            variant="secondary"
            size="xs"
            disabled={safePage >= totalPages}
            onclick={() => goTo(safePage + 1)}
        >
            Next
        </Button>
    </div>
</div>
