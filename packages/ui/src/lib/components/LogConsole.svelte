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
    }

    let {
        fetchLogs,
        chunkSize = 4096,
        lineHeight = 20,
        class: className = "",
        highlightLine = null,
    }: Props = $props();

    let flashLine = $state<number | null>(null);
    let flashTimer: ReturnType<typeof setTimeout> | null = null;

    const cache = new LogCache();
    let fetcher: LogFetcher | null = $state(null);
    let containerEl: HTMLDivElement | null = $state(null);
    let scrollTop = $state(0);
    let containerHeight = $state(0);
    let isAutoScroll = $state(true);
    let userScrolledUp = $state(false);
    let isStreaming = $derived(!cache.finished && cache.totalLines > 0);

    const OVERSCAN = 50;
    const BLANK_LINES_AT_END = 3;

    let truncationBannerHeight = $derived(cache.firstAvailableLine > 0 ? lineHeight : 0);

    // Convert a global line number to a pixel Y position.
    function lineTop(lineNum: number): number {
        return (lineNum - cache.firstAvailableLine) * lineHeight + truncationBannerHeight;
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

    let totalHeight = $derived.by(() => {
        const availableLines = cache.totalLines - cache.firstAvailableLine;
        const truncationBannerHeight = cache.firstAvailableLine > 0 ? lineHeight : 0;
        const linesHeight = availableLines * lineHeight + truncationBannerHeight;
        const streamingHeight = !cache.finished && cache.totalLines > 0 ? lineHeight : 0;
        const blankHeight = cache.totalLines > 0 ? BLANK_LINES_AT_END * lineHeight : 0;
        return Math.max(linesHeight + streamingHeight + blankHeight, containerHeight);
    });

    let gutterWidth = $derived(Math.max(4, String(cache.totalLines || 1).length) * 10 + 16);

    let renderedLines = $derived.by(() => {
        const lines: Array<{ num: number; text: string | undefined }> = [];
        const start = Math.max(cache.firstAvailableLine, visibleStart);
        const end = Math.min(cache.totalLines - 1, visibleEnd);

        for (let i = start; i <= end; i++) {
            lines.push({
                num: i,
                text: cache.lines.get(i),
            });
        }
        return lines;
    });

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

    let prevTotalLines = 0; // plain variable — intentionally non-reactive

    $effect(() => {
        const currentTotal = cache.totalLines;
        if (currentTotal > prevTotalLines && isAutoScroll && !userScrolledUp) {
            requestAnimationFrame(() => scrollToBottom());
        }
        prevTotalLines = currentTotal;
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

        const resizeObserver = new ResizeObserver(() => {
            onResize();
        });
        resizeObserver.observe(containerEl);

        return () => {
            resizeObserver.disconnect();
        };
    });
</script>

<div
    class="log-console relative flex h-full w-full flex-col overflow-hidden bg-mist-950 font-mono text-sm {className}"
>
    <div bind:this={containerEl} class="flex-1 overflow-auto" onscroll={onScroll}>
        <div class="relative" style="height: {totalHeight}px; min-height: 100%;">
            {#if cache.firstAvailableLine > 0}
                <div
                    class="absolute right-0 left-0 flex items-center bg-warning-700/20 text-warning-400"
                    style="top: 0px; height: {lineHeight}px;"
                >
                    <div
                        class="flex-shrink-0 pr-3 text-right select-none"
                        style="width: {gutterWidth}px;"
                    ></div>
                    <div class="flex-1 truncate pr-4 text-xs">
                        Log truncated: {cache.firstAvailableLine.toLocaleString()} earlier line{cache.firstAvailableLine ===
                        1
                            ? ""
                            : "s"} removed due to size limits
                    </div>
                </div>
            {/if}

            {#each renderedLines as line (line.num)}
                <div
                    class="log-line absolute right-0 left-0 flex items-center hover:bg-mist-900/50 {flashLine ===
                    line.num
                        ? 'log-line--flash'
                        : ''}"
                    style="top: {lineTop(line.num)}px; height: {lineHeight}px;"
                >
                    <div
                        class="flex-shrink-0 pr-3 text-right text-mist-500 select-none"
                        style="width: {gutterWidth}px;"
                    >
                        {line.num + 1}
                    </div>
                    <div class="flex-1 truncate pr-4 text-mist-200">
                        {#if line.text !== undefined}
                            <!-- eslint-disable-next-line svelte/no-at-html-tags -->
                            <span class="whitespace-pre">{@html ansiLineToHtml(line.text)}</span>
                        {:else}
                            <span class="text-mist-600 italic">Loading...</span>
                        {/if}
                    </div>
                </div>
            {/each}

            {#if isStreaming && cache.totalLines > 0}
                <div
                    class="streaming-indicator absolute right-0 left-0 flex items-center"
                    style="top: {lineTop(cache.totalLines)}px; height: {lineHeight}px;"
                >
                    <div
                        class="flex-shrink-0 pr-3 text-right select-none"
                        style="width: {gutterWidth}px;"
                    ></div>
                    <div class="flex items-center gap-1 text-mist-400">
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
                            cache.totalLines + (isStreaming ? 1 : 0) + i,
                        )}px; height: {lineHeight}px;"
                    >
                        <div
                            class="flex-shrink-0 pr-3 text-right text-mist-700 opacity-0 select-none"
                            style="width: {gutterWidth}px;"
                        >
                            ~
                        </div>
                    </div>
                {/each}
            {/if}

            {#if cache.totalLines === 0 && !fetcher?.isFetching}
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
    }

    .log-line--flash {
        animation: log-line-flash 1.5s ease-out;
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
