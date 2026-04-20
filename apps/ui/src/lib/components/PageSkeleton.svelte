<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    interface Props {
        /** Number of skeleton rows to show */
        rows?: number;
        /** Show a header skeleton block */
        header?: boolean;
        class?: string;
    }

    let { rows = 3, header = true, class: className = "" }: Props = $props();
</script>

<div class="animate-pulse space-y-6 p-6 {className}" role="status" aria-label="Loading content">
    {#if header}
        <div class="space-y-3">
            <div class="h-7 w-48 rounded-lg bg-mist-200"></div>
            <div class="h-4 w-72 rounded bg-mist-100"></div>
        </div>
    {/if}

    <div class="space-y-4">
        {#each Array.from({ length: rows }, (_, i) => i) as i (i)}
            <div class="space-y-2">
                <div class="h-4 rounded bg-mist-100" style="width: {70 + ((i * 17) % 30)}%"></div>
                <div class="h-4 rounded bg-mist-50" style="width: {40 + ((i * 23) % 40)}%"></div>
            </div>
        {/each}
    </div>

    <span class="sr-only">Loading...</span>
</div>
