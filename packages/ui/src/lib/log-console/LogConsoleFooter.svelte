<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { formatBytes } from "../utils/format.js";

    let {
        followTail,
        streaming,
        finished,
        fetching,
        totalLines,
        totalBytes,
        onFollow,
    }: {
        followTail: boolean;
        streaming: boolean;
        finished: boolean;
        fetching: boolean;
        totalLines: number;
        totalBytes: number;
        onFollow: () => void;
    } = $props();
</script>

{#if !followTail && totalLines > 0}
    <button
        class="absolute right-4 bottom-12 z-10 flex items-center gap-2 rounded-[3px] border
               border-[var(--rw-con-gutter)] bg-[var(--rw-con-panel)]
               px-3 py-1.5 text-xs
               text-[var(--rw-con-text)]
                                  hover:border-term-teal hover:text-term-teal active:translate-y-[1px]"
        onclick={onFollow}
    >
        <svg
            class="h-3.5 w-3.5"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
        >
            <path d="M12 5v14M19 12l-7 7-7-7" />
        </svg>
        Scroll to bottom
    </button>
{/if}

<div
    class="flex flex-shrink-0 items-center justify-between border-t border-[var(--rw-con-gutter)]
           bg-[var(--rw-con-panel)] px-3.5 py-2 text-[11px] tracking-wide text-[var(--rw-con-dim)]"
>
    <div class="flex items-center gap-3">
        {#if streaming}
            <div class="flex items-center gap-1.5 text-aurora-400">
                <div class="h-1.5 w-1.5 animate-pulse rounded-full bg-aurora-400"></div>
                Streaming
            </div>
        {:else if finished}
            <span>Stream ended</span>
        {/if}
        {#if fetching}
            <span>Fetching...</span>
        {/if}
    </div>
    <div class="flex items-center gap-4">
        <span>{totalLines.toLocaleString()} lines</span>
        {#if totalBytes > 0}
            <span>{formatBytes(totalBytes)}</span>
        {/if}
        {#if followTail}
            <span class="inline-flex items-center gap-1.5 text-term-ok">
                <svg
                    class="h-3 w-3"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    stroke-width="2.2"
                    stroke-linecap="round"
                    stroke-linejoin="round"
                >
                    <line x1="12" y1="5" x2="12" y2="19" />
                    <polyline points="6 13 12 19 18 13" />
                </svg>
                Auto-scroll
            </span>
        {/if}
    </div>
</div>
