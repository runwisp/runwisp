<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    type SkeletonShape = "line" | "circle" | "rect";

    interface Props {
        /** Shape variant */
        shape?: SkeletonShape;
        /** Number of skeleton rows (only for shape="line") */
        rows?: number;
        /** Show a header skeleton block (only for shape="line") */
        header?: boolean;
        /** Width (for circle/rect) */
        width?: string;
        /** Height (for circle/rect) */
        height?: string;
        class?: string;
    }

    let {
        shape = "line",
        rows = 3,
        header = true,
        width,
        height,
        class: className = "",
    }: Props = $props();
</script>

{#if shape === "circle"}
    <div
        class="animate-pulse rounded-full bg-outline {className}"
        style="width: {width ?? '2.5rem'}; height: {height ?? width ?? '2.5rem'}"
        role="status"
        aria-label="Loading"
    >
        <span class="sr-only">Loading...</span>
    </div>
{:else if shape === "rect"}
    <div
        class="animate-pulse rounded-lg bg-outline {className}"
        style="width: {width ?? '100%'}; height: {height ?? '2rem'}"
        role="status"
        aria-label="Loading"
    >
        <span class="sr-only">Loading...</span>
    </div>
{:else}
    <div class="animate-pulse space-y-6 p-6 {className}" role="status" aria-label="Loading content">
        {#if header}
            <div class="space-y-3">
                <div class="h-7 w-48 rounded-lg bg-outline"></div>
                <div class="h-4 w-72 rounded bg-outline-faint"></div>
            </div>
        {/if}

        <div class="space-y-4">
            {#each Array.from({ length: rows }, (_, i) => i) as i (i)}
                <div class="space-y-2">
                    <div
                        class="h-4 rounded bg-outline-faint"
                        style="width: {70 + ((i * 17) % 30)}%"
                    ></div>
                    <div
                        class="h-4 rounded bg-surface-sunken"
                        style="width: {40 + ((i * 23) % 40)}%"
                    ></div>
                </div>
            {/each}
        </div>

        <span class="sr-only">Loading...</span>
    </div>
{/if}
