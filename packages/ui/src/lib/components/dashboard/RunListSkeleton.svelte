<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    let { label, showTaskName }: { label: string; showTaskName: boolean } = $props();
</script>

<!-- Placeholders shaped like the list's run rows (same padding, dot, text and
     right readout), while a search or the first page is in flight. -->
<div class="flex flex-col gap-0.5" role="status" aria-busy="true" aria-label={label}>
    {#each [0, 1, 2, 3, 4, 5] as i (i)}
        <div
            class="rounded-[3px] border border-transparent {showTaskName
                ? 'p-3'
                : 'px-3 py-[11px]'}"
        >
            <div class="flex items-center {showTaskName ? 'gap-2.5' : 'gap-[11px]'}">
                <span class="size-[9px] shrink-0 animate-pulse rounded-full bg-outline-hover"
                ></span>
                {#if showTaskName}
                    <span class="flex min-w-0 flex-1 flex-col gap-1.5 py-0.5">
                        <span
                            class="h-3 animate-pulse rounded-[3px] bg-outline-hover"
                            style:width="{62 - (i % 3) * 12}%"
                        ></span>
                        <span class="h-2 w-2/5 animate-pulse rounded-[3px] bg-outline-hover"></span>
                    </span>
                    <span
                        class="h-2.5 w-9 shrink-0 animate-pulse self-start rounded-[3px] bg-outline-hover"
                    ></span>
                {:else}
                    <span
                        class="h-3 animate-pulse rounded-[3px] bg-outline-hover"
                        style:width="{58 - (i % 3) * 8}%"
                    ></span>
                    <span
                        class="ml-auto h-2.5 w-9 shrink-0 animate-pulse rounded-[3px] bg-outline-hover"
                    ></span>
                {/if}
            </div>
        </div>
    {/each}
</div>
