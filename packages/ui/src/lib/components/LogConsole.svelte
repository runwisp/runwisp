<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { untrack } from "svelte";
    import Convert from "ansi-to-html";
    import { LogCache } from "../log-console/LogCache.svelte.js";
    import { LogFetcher } from "../log-console/LogFetcher.svelte.js";
    import type { FetchLogsFn, LogEvent } from "../log-console/types.js";
    import { formatBytes } from "../utils/format.js";

    // Convert ANSI escape sequences in a single log line to safe HTML spans.
    // A fresh converter per call ensures no colour state leaks between
    // independently virtualised lines.
    function ansiLineToHtml(text: string): string {
        return new Convert({ escapeXML: true }).toHtml(text);
    }

    interface Props {
        fetchLogs?: FetchLogsFn;
        chunkSize?: number;
        lineHeight?: number;
        class?: string;
    }

    let { fetchLogs, chunkSize = 4096, lineHeight = 20, class: className = "" }: Props = $props();

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
        if (fetcher && cache.totalLines > 0) {
            fetcher.pruneQueue(visibleStart, visibleEnd);
            untrack(() => {
                fetcher!.maybeRequestMissing(visibleStart, visibleEnd);
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

    function onScroll(e: Event) {
        const target = e.target as HTMLDivElement;
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
    class="log-console relative flex h-full w-full flex-col overflow-hidden bg-slate-950 font-mono text-sm {className}"
>
    <div bind:this={containerEl} class="flex-1 overflow-auto" onscroll={onScroll}>
        <div class="relative" style="height: {totalHeight}px; min-height: 100%;">
            {#if cache.firstAvailableLine > 0}
                <div
                    class="absolute right-0 left-0 flex items-center bg-amber-950/60 text-amber-400"
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
                    class="log-line absolute right-0 left-0 flex items-center hover:bg-slate-900/50"
                    style="top: {lineTop(line.num)}px; height: {lineHeight}px;"
                >
                    <div
                        class="flex-shrink-0 pr-3 text-right text-slate-600 select-none"
                        style="width: {gutterWidth}px;"
                    >
                        {line.num + 1}
                    </div>
                    <div class="flex-1 truncate pr-4 text-slate-300">
                        {#if line.text !== undefined}
                            <!-- eslint-disable-next-line svelte/no-at-html-tags -->
                            <span class="whitespace-pre">{@html ansiLineToHtml(line.text)}</span>
                        {:else}
                            <span class="text-slate-700 italic">Loading...</span>
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
                    <div class="flex items-center gap-1 text-slate-500">
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
                            class="flex-shrink-0 pr-3 text-right text-slate-800 opacity-0 select-none"
                            style="width: {gutterWidth}px;"
                        >
                            ~
                        </div>
                    </div>
                {/each}
            {/if}

            {#if cache.totalLines === 0 && !fetcher?.isFetching}
                <div class="absolute inset-0 flex items-center justify-center text-slate-600">
                    <div class="text-center">
                        <div class="mb-1 text-lg">No output yet</div>
                        <div class="text-sm text-slate-700">Waiting for logs...</div>
                    </div>
                </div>
            {/if}

            {#if cache.totalLines === 0 && fetcher?.isFetching}
                <div class="absolute inset-0 flex items-center justify-center text-slate-600">
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
                   border-slate-700 bg-slate-800
                   px-3 py-1.5 text-xs
                   font-medium text-slate-300 shadow-lg
                   transition-all duration-200 hover:scale-105
                   hover:bg-slate-700 active:scale-95"
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
        class="flex flex-shrink-0 items-center justify-between border-t border-slate-800
               bg-slate-900/80 px-3 py-1.5 text-xs text-slate-500"
    >
        <div class="flex items-center gap-3">
            {#if isStreaming}
                <div class="flex items-center gap-1.5 text-aurora-400">
                    <div class="h-1.5 w-1.5 animate-pulse rounded-full bg-aurora-400"></div>
                    Streaming
                </div>
            {:else if cache.finished}
                <span class="text-slate-600">Stream ended</span>
            {/if}
            {#if fetcher?.isFetching}
                <span class="text-slate-600">Fetching...</span>
            {/if}
        </div>
        <div class="flex items-center gap-4">
            <span>{cache.totalLines.toLocaleString()} lines</span>
            {#if cache.totalBytes > 0}
                <span>{formatBytes(cache.totalBytes)}</span>
            {/if}
            {#if isAutoScroll}
                <span class="text-aurora-500">Auto-scroll</span>
            {/if}
        </div>
    </div>
</div>

<style>
    .log-console {
        --log-line-height: 20px;
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
