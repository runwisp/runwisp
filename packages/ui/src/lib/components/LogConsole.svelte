<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { untrack } from "svelte";
    import { ansiLineToHtml } from "../log-console/ansi.js";
    import { LogCache } from "../log-console/LogCache.svelte.js";
    import { LogFetcher } from "../log-console/LogFetcher.svelte.js";
    import type { FetchLogsFn, LogEvent } from "../log-console/types.js";
    import { formatBytes } from "../utils/format.js";

    interface Props {
        fetchLogs?: FetchLogsFn;
        chunkSize?: number;
        lineHeight?: number;
        class?: string;
        // highlightLine, when set, scrolls the console so the given absolute
        // line number is visible and pulses a short-lived flash on that row.
        // The pulse is one-shot: changing the prop to a new line restarts the
        // animation; setting it to null clears immediately.
        highlightLine?: number | null;
        // fetchLineHistory resolves the prior whole-region frames a settled
        // progress bar / multi-line redraw passed through, for the given absolute
        // line number. When omitted, anchor lines render without the rewind
        // affordance.
        fetchLineHistory?: ((lineNum: number) => Promise<string[][]>) | undefined;
    }

    let {
        fetchLogs,
        chunkSize = 4096,
        lineHeight = 20,
        class: className = "",
        highlightLine = null,
        fetchLineHistory,
    }: Props = $props();

    // --- Frame-history inline expansion (single expansion at a time) ---
    // expandedLine is the absolute line number whose history block is open;
    // expandedFrames holds the fetched whole-region frames (null while loading).
    let expandedLine = $state<number | null>(null);
    let expandedFrames = $state<string[][] | null>(null);
    let expandedError = $state(false);

    const FRAME_BLOCK_PAD = 8; // px padding above+below the block content
    const FRAME_GAP = 6; // px between consecutive frames
    const FRAME_BLOCK_MAX = 400; // px cap; the block scrolls internally beyond this

    // Height the open history block occupies in the virtual surface. Subsequent
    // lines are shifted down by exactly this, so the math stays a single offset.
    let frameBlockHeight = $derived.by(() => {
        if (expandedLine === null) return 0;
        if (!expandedFrames) return lineHeight * 2; // loading / error placeholder
        let h = FRAME_BLOCK_PAD * 2;
        for (const frame of expandedFrames) {
            h += lineHeight; // per-frame label
            h += frame.length * lineHeight; // the frame's rows
        }
        if (expandedFrames.length > 1) h += FRAME_GAP * (expandedFrames.length - 1);
        return Math.min(h, FRAME_BLOCK_MAX);
    });

    function collapseHistory() {
        expandedLine = null;
        expandedFrames = null;
        expandedError = false;
    }

    async function toggleHistory(lineNum: number) {
        if (expandedLine === lineNum) {
            collapseHistory();
            return;
        }
        expandedLine = lineNum;
        expandedFrames = null;
        expandedError = false;
        const fn = fetchLineHistory;
        if (!fn) return;
        try {
            const frames = await fn(lineNum);
            if (expandedLine === lineNum) expandedFrames = frames;
        } catch {
            if (expandedLine === lineNum) expandedError = true;
        }
    }

    let flashLine = $state<number | null>(null);
    let flashTimer: ReturnType<typeof setTimeout> | null = null;

    const cache = new LogCache();
    let fetcher: LogFetcher | null = $state(null);
    let containerEl: HTMLDivElement | null = $state(null);
    let rulerEl: HTMLSpanElement | null = $state(null);
    let scrollTop = $state(0);
    let containerHeight = $state(0);
    let containerWidth = $state(0);
    // Monospace character width in px, measured from a hidden ruler so the
    // horizontal scroll surface is sized without per-line DOM reflow.
    let charWidth = $state(8);
    let isAutoScroll = $state(true);
    let userScrolledUp = $state(false);
    let isStreaming = $derived(!cache.finished && cache.totalLines > 0);

    const OVERSCAN = 50;
    const BLANK_LINES_AT_END = 3;
    const RULER_SAMPLE = "0".repeat(50);
    // Trailing slack past the last column so the widest line never sits flush
    // against the scroll edge.
    const SURFACE_PADDING = 16;

    let truncationBannerHeight = $derived(cache.firstAvailableLine > 0 ? lineHeight : 0);

    // Convert a global line number to a pixel Y position. Lines below an open
    // history block are pushed down by the block's height (single expansion, so
    // it's one conditional rather than a prefix-sum).
    function lineTop(lineNum: number): number {
        const base = (lineNum - cache.firstAvailableLine) * lineHeight + truncationBannerHeight;
        if (expandedLine !== null && lineNum > expandedLine) return base + frameBlockHeight;
        return base;
    }

    let visibleStart = $derived(
        Math.max(
            cache.firstAvailableLine,
            cache.firstAvailableLine +
                Math.floor(Math.max(0, scrollTop - truncationBannerHeight) / lineHeight) -
                OVERSCAN,
        ),
    );
    let visibleEnd = $derived(
        Math.max(
            cache.firstAvailableLine,
            Math.min(
                Math.max(0, cache.totalLines - 1),
                cache.firstAvailableLine +
                    Math.ceil(
                        Math.max(0, scrollTop - truncationBannerHeight + containerHeight) /
                            lineHeight,
                    ) +
                    OVERSCAN,
            ),
        ),
    );

    // Live-region overlay rows, rendered in place below the committed lines.
    let overlayRows = $derived(cache.overlayRows);

    let totalHeight = $derived.by(() => {
        const availableLines = cache.totalLines - cache.firstAvailableLine;
        const truncationBannerHeight = cache.firstAvailableLine > 0 ? lineHeight : 0;
        const linesHeight = availableLines * lineHeight + truncationBannerHeight;
        const overlayHeight = overlayRows.length * lineHeight;
        const streamingHeight = !cache.finished && cache.totalLines > 0 ? lineHeight : 0;
        const blankHeight = cache.totalLines > 0 ? BLANK_LINES_AT_END * lineHeight : 0;
        return Math.max(
            linesHeight + overlayHeight + streamingHeight + blankHeight + frameBlockHeight,
            containerHeight,
        );
    });

    let gutterWidth = $derived(Math.max(4, String(cache.totalLines || 1).length) * 10 + 16);

    // Width of the virtual scroll surface: wide enough for the longest line
    // seen so far, never narrower than the viewport (so short logs show no
    // horizontal scrollbar).
    let surfaceWidth = $derived(
        Math.max(containerWidth, gutterWidth + cache.maxLineColumns * charWidth + SURFACE_PADDING),
    );

    let renderedLines = $derived.by(() => {
        const lines: Array<{ num: number; text: string | undefined; frameCount: number }> = [];
        const start = Math.max(cache.firstAvailableLine, visibleStart);
        const end = Math.min(cache.totalLines - 1, visibleEnd);

        for (let i = start; i <= end; i++) {
            lines.push({
                num: i,
                text: cache.lines.get(i),
                frameCount: cache.frameCounts.get(i) ?? 0,
            });
        }
        return lines;
    });

    // Anchors are clickable only when the parent supplied a history fetcher.
    let canExpand = $derived(fetchLineHistory !== undefined);

    // The fetcher serves on-demand scroll-up loads only; the parent seeds
    // the initial cache via onStream (typically a tail fetch) so the
    // viewport lands at the end of the log without racing the auto-scroll.
    $effect(() => {
        const fn = fetchLogs;
        const cs = chunkSize;
        fetcher = new LogFetcher(cache, fn, cs, () => {
            if (isAutoScroll && !userScrolledUp) {
                requestAnimationFrame(() => scrollToBottom());
            }
        });

        return () => {
            fetcher?.destroy();
        };
    });

    // Request missing data for the visible range (read-only w.r.t. cache).
    $effect(() => {
        const f = fetcher;
        if (f && cache.totalLines > 0) {
            f.pruneQueue(visibleStart, visibleEnd);
            untrack(() => {
                f.maybeRequestMissing(visibleStart, visibleEnd);
            });
        }
    });

    // Prune stale cache entries — separate from reads to avoid a read-write cycle on SvelteMap.
    $effect(() => {
        const vs = visibleStart;
        const ve = visibleEnd;
        untrack(() => cache.prune(vs, ve));
    });

    let prevTailRows = 0; // plain variable — intentionally non-reactive

    $effect(() => {
        // Track committed lines AND live overlay rows so an animating region at
        // the tail keeps a bottom-anchored viewport pinned to the bottom.
        const currentTail = cache.totalLines + overlayRows.length;
        if (currentTail > prevTailRows && isAutoScroll && !userScrolledUp) {
            requestAnimationFrame(() => scrollToBottom());
        }
        prevTailRows = currentTail;
    });

    function onScroll(e: Event & { currentTarget: EventTarget & HTMLDivElement }) {
        const target = e.currentTarget;
        scrollTop = target.scrollTop;

        const scrollHeight = target.scrollHeight;
        const clientHeight = target.clientHeight;
        const currentScroll = target.scrollTop;
        const distanceFromBottom = scrollHeight - currentScroll - clientHeight;

        const atBottom = distanceFromBottom < lineHeight * 2;

        if (atBottom) {
            userScrolledUp = false;
            isAutoScroll = true;
        } else if (isAutoScroll) {
            userScrolledUp = true;
            isAutoScroll = false;
        }
    }

    function scrollToBottom(smooth = false) {
        if (containerEl) {
            if (smooth) {
                containerEl.scrollTo({
                    top: containerEl.scrollHeight,
                    behavior: "smooth",
                });
            } else {
                containerEl.scrollTop = containerEl.scrollHeight;
            }
        }
    }

    function enableAutoScroll() {
        isAutoScroll = true;
        userScrolledUp = false;
        scrollToBottom();
    }

    function onResize() {
        if (containerEl) {
            containerHeight = containerEl.clientHeight;
            containerWidth = containerEl.clientWidth;
        }
        measureCharWidth();
    }

    // Measure one monospace column from the hidden ruler. Font, zoom, and
    // DPI changes all surface through the ResizeObserver, so this stays
    // accurate without polling.
    function measureCharWidth() {
        if (rulerEl && rulerEl.offsetWidth > 0) {
            charWidth = rulerEl.offsetWidth / RULER_SAMPLE.length;
        }
    }

    export function onStream(event: LogEvent) {
        const prevTotal = cache.totalLines;
        cache.applyEvent(event);

        if (cache.totalLines > prevTotal && isAutoScroll && !userScrolledUp) {
            requestAnimationFrame(() => scrollToBottom());
        }

        if (fetcher) {
            fetcher.maybeRequestMissing(visibleStart, visibleEnd);
        }
    }

    export function reset() {
        cache.reset();
        collapseHistory();
        isAutoScroll = true;
        userScrolledUp = false;
        scrollTop = 0;
    }

    // Search-hit deep-link: when highlightLine flips to a non-null number,
    // scroll the viewport so that line is centred, and pulse a one-shot
    // flash class for ~1.5s. Cleaning up the timer on prop change keeps a
    // rapid sequence of jumps from leaking timers.
    $effect(() => {
        const target = highlightLine;
        if (target === null || target === undefined) {
            flashLine = null;
            return;
        }
        if (!containerEl) return;
        const targetY = lineTop(target) - containerHeight / 2 + lineHeight / 2;
        const clamped = Math.max(0, Math.min(targetY, totalHeight - containerHeight));
        containerEl.scrollTo({ top: clamped, behavior: "smooth" });
        isAutoScroll = false;
        userScrolledUp = true;
        flashLine = target;
        if (flashTimer !== null) clearTimeout(flashTimer);
        flashTimer = setTimeout(() => {
            flashLine = null;
            flashTimer = null;
        }, 1500);
    });

    $effect(() => {
        if (!containerEl) return;
        containerHeight = containerEl.clientHeight;
        containerWidth = containerEl.clientWidth;
        measureCharWidth();

        const resizeObserver = new ResizeObserver(() => {
            onResize();
        });
        resizeObserver.observe(containerEl);

        return () => {
            resizeObserver.disconnect();
        };
    });
</script>

<!-- One place that renders ANSI-converted markup, so the necessary @html escape
     hatch (the input is sanitised by ansiLineToHtml) lives behind a single
     audited disable rather than being repeated per call site. -->
{#snippet ansiLine(text: string)}
    <!-- eslint-disable-next-line svelte/no-at-html-tags -->
    <span class="whitespace-pre">{@html ansiLineToHtml(text)}</span>
{/snippet}

<div
    class="log-console relative flex h-full w-full flex-col overflow-hidden bg-mist-950 font-mono text-sm {className}"
>
    <!-- Hidden ruler: one monospace column is measured from this off-screen
         sample to size the horizontal scroll surface without per-line reflow. -->
    <span
        bind:this={rulerEl}
        aria-hidden="true"
        class="pointer-events-none absolute -top-[9999px] left-0 whitespace-pre select-none"
        >{RULER_SAMPLE}</span
    >

    <div bind:this={containerEl} class="flex-1 overflow-auto" onscroll={onScroll}>
        <div
            class="relative"
            style="height: {totalHeight}px; min-height: 100%; width: {surfaceWidth}px; min-width: 100%;"
        >
            {#if cache.firstAvailableLine > 0}
                <div
                    class="absolute right-0 left-0 flex items-center bg-warning-700/20 text-warning-400"
                    style="top: 0px; height: {lineHeight}px;"
                >
                    <div
                        class="sticky left-0 z-10 flex-shrink-0 pr-3 text-right select-none"
                        style="width: {gutterWidth}px;"
                    ></div>
                    <div class="sticky flex-shrink-0 pr-4 text-xs" style="left: {gutterWidth}px;">
                        Log truncated: {cache.firstAvailableLine.toLocaleString()} earlier line{cache.firstAvailableLine ===
                        1
                            ? ""
                            : "s"} removed due to size limits
                    </div>
                </div>
            {/if}

            {#each renderedLines as line (line.num)}
                {@const isAnchor = canExpand && line.frameCount > 0}
                <div
                    class="log-line absolute right-0 left-0 flex items-center hover:bg-mist-900/50 {flashLine ===
                    line.num
                        ? 'log-line--flash'
                        : ''}"
                    style="top: {lineTop(line.num)}px; height: {lineHeight}px;"
                >
                    <div
                        class="sticky left-0 z-10 flex flex-shrink-0 items-center justify-end gap-1 bg-mist-950 pr-3 text-right text-mist-500 select-none"
                        style="width: {gutterWidth}px;"
                    >
                        {#if isAnchor}
                            <button
                                type="button"
                                class="frame-toggle {expandedLine === line.num
                                    ? 'text-aurora-400'
                                    : 'text-mist-500 hover:text-aurora-400'}"
                                title="{line.frameCount} earlier frame{line.frameCount === 1
                                    ? ''
                                    : 's'} — click to {expandedLine === line.num
                                    ? 'hide'
                                    : 'rewind'}"
                                aria-label="Toggle frame history for line {line.num + 1}"
                                aria-expanded={expandedLine === line.num}
                                onclick={() => toggleHistory(line.num)}
                            >
                                ↻
                            </button>
                        {/if}
                        {line.num + 1}
                    </div>
                    <div class="flex-1 pr-4 text-mist-200">
                        {#if line.text !== undefined}
                            {@render ansiLine(line.text)}
                        {:else}
                            <span class="text-mist-600 italic">Loading...</span>
                        {/if}
                    </div>
                </div>
            {/each}

            {#if expandedLine !== null}
                <div
                    class="frame-history absolute right-0 left-0 overflow-y-auto border-y border-mist-800 bg-mist-900/40"
                    style="top: {lineTop(expandedLine) +
                        lineHeight}px; height: {frameBlockHeight}px;"
                >
                    <div style="padding: {FRAME_BLOCK_PAD}px 0;">
                        {#if expandedFrames}
                            {#each expandedFrames as frame, fi (fi)}
                                <div style="margin-top: {fi > 0 ? FRAME_GAP : 0}px;">
                                    <div
                                        class="px-3 text-xs text-mist-500 select-none"
                                        style="height: {lineHeight}px; line-height: {lineHeight}px;"
                                    >
                                        Frame {fi + 1} of {expandedFrames.length}
                                    </div>
                                    {#each frame as row, ri (ri)}
                                        <div
                                            class="flex items-center"
                                            style="height: {lineHeight}px;"
                                        >
                                            <div
                                                class="flex-shrink-0 pr-3 text-right text-mist-700 select-none"
                                                style="width: {gutterWidth}px;"
                                            ></div>
                                            <div class="flex-1 pr-4 text-mist-300">
                                                {@render ansiLine(row)}
                                            </div>
                                        </div>
                                    {/each}
                                </div>
                            {/each}
                        {:else}
                            <div
                                class="px-3 text-xs text-mist-500 italic"
                                style="line-height: {lineHeight}px; padding-left: {gutterWidth}px;"
                            >
                                {expandedError ? "Failed to load frame history" : "Loading frames…"}
                            </div>
                        {/if}
                    </div>
                </div>
            {/if}

            {#each overlayRows as row, i (i)}
                <div
                    class="log-line log-line--live absolute right-0 left-0 flex items-center"
                    style="top: {lineTop(cache.totalLines + i)}px; height: {lineHeight}px;"
                >
                    <div
                        class="sticky left-0 z-10 flex-shrink-0 bg-mist-950 pr-3 text-right text-mist-600 select-none"
                        style="width: {gutterWidth}px;"
                    >
                        ⋯
                    </div>
                    <div class="flex-1 pr-4 text-mist-200">
                        {@render ansiLine(row)}
                    </div>
                </div>
            {/each}

            {#if isStreaming && cache.totalLines > 0}
                <div
                    class="streaming-indicator absolute right-0 left-0 flex items-center"
                    style="top: {lineTop(
                        cache.totalLines + overlayRows.length,
                    )}px; height: {lineHeight}px;"
                >
                    <div
                        class="sticky left-0 z-10 flex-shrink-0 bg-mist-950 pr-3 text-right select-none"
                        style="width: {gutterWidth}px;"
                    ></div>
                    <div
                        class="sticky flex items-center gap-1 text-mist-400"
                        style="left: {gutterWidth}px;"
                    >
                        <span class="dot" style="animation-delay: 0ms;"></span>
                        <span class="dot" style="animation-delay: 150ms;"></span>
                        <span class="dot" style="animation-delay: 300ms;"></span>
                    </div>
                </div>
            {/if}

            {#if cache.totalLines > 0}
                {#each Array.from({ length: BLANK_LINES_AT_END }, (_, i) => i) as i (i)}
                    <div
                        class="absolute right-0 left-0 flex items-center"
                        style="top: {lineTop(
                            cache.totalLines + overlayRows.length + (isStreaming ? 1 : 0) + i,
                        )}px; height: {lineHeight}px;"
                    >
                        <div
                            class="sticky left-0 z-10 flex-shrink-0 bg-mist-950 pr-3 text-right text-mist-700 opacity-0 select-none"
                            style="width: {gutterWidth}px;"
                        >
                            ~
                        </div>
                    </div>
                {/each}
            {/if}

            {#if cache.totalLines === 0 && overlayRows.length === 0 && !fetcher?.isFetching}
                <div class="absolute inset-0 flex items-center justify-center text-mist-500">
                    <div class="text-center">
                        <div class="mb-1 text-lg">No output yet</div>
                        <div class="text-sm text-mist-600">Waiting for logs...</div>
                    </div>
                </div>
            {/if}

            {#if cache.totalLines === 0 && fetcher?.isFetching}
                <div class="absolute inset-0 flex items-center justify-center text-mist-500">
                    <div class="flex items-center gap-2">
                        <span class="dot" style="animation-delay: 0ms;"></span>
                        <span class="dot" style="animation-delay: 150ms;"></span>
                        <span class="dot" style="animation-delay: 300ms;"></span>
                        <span class="ml-2">Loading logs...</span>
                    </div>
                </div>
            {/if}
        </div>
    </div>

    {#if userScrolledUp && cache.totalLines > 0}
        <button
            class="absolute right-4 bottom-12 z-10 flex items-center gap-2 rounded-full border
                   border-mist-700 bg-mist-800
                   px-3 py-1.5 text-xs
                   font-medium text-mist-200 shadow-lg
                   transition-all duration-200 hover:scale-105
                   hover:bg-mist-700 active:scale-95"
            onclick={enableAutoScroll}
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
        class="flex flex-shrink-0 items-center justify-between border-t border-mist-800
               bg-mist-900/80 px-3 py-1.5 text-xs text-mist-400"
    >
        <div class="flex items-center gap-3">
            {#if isStreaming}
                <div class="flex items-center gap-1.5 text-info">
                    <div class="h-1.5 w-1.5 animate-pulse rounded-full bg-aurora-400"></div>
                    Streaming
                </div>
            {:else if cache.finished}
                <span class="text-mist-500">Stream ended</span>
            {/if}
            {#if fetcher?.isFetching}
                <span class="text-mist-500">Fetching...</span>
            {/if}
        </div>
        <div class="flex items-center gap-4">
            <span>{cache.totalLines.toLocaleString()} lines</span>
            {#if cache.totalBytes > 0}
                <span>{formatBytes(cache.totalBytes)}</span>
            {/if}
            {#if isAutoScroll}
                <span class="text-info">Auto-scroll</span>
            {/if}
        </div>
    </div>
</div>

<style>
    .log-console {
        --log-line-height: 20px;
        tab-size: 8;
    }

    .log-line--flash {
        animation: log-line-flash 1.5s ease-out;
    }

    .frame-toggle {
        cursor: pointer;
        font-size: 0.85em;
        line-height: 1;
        background: transparent;
        border: none;
        padding: 0;
        transition: color 120ms ease;
    }

    @keyframes log-line-flash {
        0% {
            background-color: oklch(0.8 0.18 70 / 0.45);
        }
        100% {
            background-color: transparent;
        }
    }

    .dot {
        display: inline-block;
        width: 4px;
        height: 4px;
        border-radius: 50%;
        background-color: currentColor;
        animation: dot-pulse 1s ease-in-out infinite;
    }

    @keyframes dot-pulse {
        0%,
        60%,
        100% {
            opacity: 0.3;
            transform: scale(0.8);
        }
        30% {
            opacity: 1;
            transform: scale(1);
        }
    }

    .log-console ::-webkit-scrollbar {
        width: 10px;
        height: 10px;
    }

    .log-console ::-webkit-scrollbar-track {
        background: transparent;
    }

    .log-console ::-webkit-scrollbar-thumb {
        background-color: var(--color-mist-700);
        border-radius: 5px;
        border: 2px solid transparent;
        background-clip: content-box;
    }

    .log-console ::-webkit-scrollbar-thumb:hover {
        background-color: var(--color-mist-600);
        background-clip: content-box;
    }

    .log-console ::-webkit-scrollbar-corner {
        background: transparent;
    }
</style>
