<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import AnsiLine from "./AnsiLine.svelte";
    import { FRAME_BLOCK_PAD, FRAME_GAP, type FrameHistory } from "./frame-history.svelte.js";
    import { LINE_HEIGHT } from "./wrap-layout.svelte.js";

    let {
        history,
        top,
        gutterWidth,
    }: {
        history: FrameHistory;
        /** Distance from the top of the virtual surface, px. */
        top: number;
        gutterWidth: number;
    } = $props();
</script>

<div
    class="frame-history absolute right-0 left-0 overflow-y-auto border-y border-[var(--rw-con-gutter)] bg-[var(--rw-con-panel)]"
    style="top: {top}px; height: {history.blockHeight}px;"
>
    <div style="padding: {FRAME_BLOCK_PAD}px 0;">
        {#if history.frames}
            {#each history.frames as frame, fi (fi)}
                <div style="margin-top: {fi > 0 ? FRAME_GAP : 0}px;">
                    <div
                        class="px-3 text-xs text-[var(--rw-con-dim)] select-none"
                        style="height: {LINE_HEIGHT}px; line-height: {LINE_HEIGHT}px;"
                    >
                        Frame {fi + 1} of {history.frames.length}
                    </div>
                    {#each frame as row, ri (ri)}
                        <div class="flex items-center" style="height: {LINE_HEIGHT}px;">
                            <div
                                class="flex-shrink-0 pr-3 text-right text-[var(--rw-con-gutter)] select-none"
                                style="width: {gutterWidth}px;"
                            ></div>
                            <div class="flex-1 pr-4 text-[var(--rw-con-text)]">
                                <AnsiLine text={row} />
                            </div>
                        </div>
                    {/each}
                </div>
            {/each}
        {:else}
            <div
                class="px-3 text-xs text-[var(--rw-con-dim)] italic"
                style="line-height: {LINE_HEIGHT}px; padding-left: {gutterWidth}px;"
            >
                {history.failed ? "Failed to load frame history" : "Loading frames…"}
            </div>
        {/if}
    </div>
</div>
