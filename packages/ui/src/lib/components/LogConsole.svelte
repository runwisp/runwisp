<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { untrack } from "svelte";
    import { ansiLineToHtml } from "../log-console/ansi.js";
    import AnsiLine from "../log-console/AnsiLine.svelte";
    import FrameHistoryBlock from "../log-console/FrameHistoryBlock.svelte";
    import LogConsoleFooter from "../log-console/LogConsoleFooter.svelte";
    import { FrameHistory } from "../log-console/frame-history.svelte.js";
    import { LogCache } from "../log-console/LogCache.svelte.js";
    import { LogFetcher } from "../log-console/LogFetcher.svelte.js";
    import { createHighlightScroll } from "../log-console/highlight-scroll.js";
    import type { FetchLogsFn, LogEvent } from "../log-console/types.js";
    import { LINE_HEIGHT as lineHeight, WrapLayout } from "../log-console/wrap-layout.svelte.js";

    interface Props {
        fetchLogs?: FetchLogsFn;
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
        // Text of the terminus marker drawn once a finished log's last line is
        // reached (default "end of output"). A stopped/timed-out run passes its
        // own wording so a short capture ends on an explicit reason, not a void.
        endLabel?: string;
        // "muted" (default) renders the terminus in the gutter grey; "warn"
        // tints it amber for runs that ended by intervention rather than naturally.
        endTone?: "muted" | "warn";
        // Message from the most recent fetchLogs failure (session expiry,
        // daemon restart mid-request, network blip, ...). Rendered as a small
        // banner above the log surface so a fetch failure never looks like a
        // run that simply produced no output. Null/undefined renders nothing;
        // the caller clears it once a subsequent fetch succeeds.
        error?: string | null;
        // The caller is still fetching this console's first output (e.g. the
        // run panel seeding its tail). Shows the loading state rather than
        // "No output yet" until that output arrives.
        loading?: boolean;
        // Wrap long lines instead of horizontally scrolling them. When on, each
        // rendered row's height becomes a multiple of `lineHeight` (one per
        // wrapped visual row), so the virtualizer switches from the fixed-height
        // linear layout to a prefix-sum-of-row-counts layout. Off by default to
        // preserve the original horizontal-scroll behaviour for wide output.
        wrap?: boolean;
    }

    let {
        fetchLogs,
        class: className = "",
        highlightLine = null,
        fetchLineHistory,
        endLabel = "end of output",
        endTone = "muted",
        error = null,
        loading = false,
        wrap = $bindable(false),
    }: Props = $props();

    const frames = new FrameHistory(() => fetchLineHistory);

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
    // True while the viewport is pinned to the end of the log: new lines scroll
    // into view. Scrolling away from the bottom turns it off.
    let followTail = $state(true);
    let isStreaming = $derived(!cache.finished && cache.totalLines > 0);

    const OVERSCAN = 50;
    const BLANK_LINES_AT_END = 3;
    const RULER_SAMPLE = "0".repeat(50);
    // Trailing slack past the last column so the widest line never sits flush
    // against the scroll edge.
    const SURFACE_PADDING = 16;

    let truncationBannerHeight = $derived(cache.firstAvailableLine > 0 ? lineHeight : 0);

    let gutterWidth = $derived(Math.max(4, String(cache.totalLines || 1).length) * 10 + 16);

    const layout = new WrapLayout(cache, {
        wrap: () => wrap,
        width: () => containerWidth,
        gutterWidth: () => gutterWidth,
        charWidth: () => charWidth,
    });

    // Line-text white-space model: wrapped lines break anywhere (so a long
    // token wraps mid-word the way a terminal would), unwrapped lines stay on
    // one horizontal-scrolling row.
    let lineTextClass = $derived(wrap ? "break-anywhere whitespace-pre-wrap" : "whitespace-pre");

    // Convert a global line number to a pixel Y position. Lines below an open
    // history block are pushed down by the block's height (single expansion).
    function lineTop(lineNum: number): number {
        const base =
            truncationBannerHeight +
            (frames.line !== null && lineNum > frames.line ? frames.blockHeight : 0);
        return layout.rowsAbove(lineNum) * lineHeight + base;
    }

    function lineHeightPx(lineNum: number): number {
        return layout.rowCount(lineNum) * lineHeight;
    }

    // Scroll offset past the truncation banner, in rows.
    let scrollRows = $derived(Math.max(0, scrollTop - truncationBannerHeight) / lineHeight);

    let visibleStart = $derived(
        Math.max(
            cache.firstAvailableLine,
            cache.firstAvailableLine + layout.offsetAt(scrollRows) - OVERSCAN,
        ),
    );

    let visibleEnd = $derived(
        Math.max(
            cache.firstAvailableLine,
            Math.min(
                Math.max(0, cache.totalLines - 1),
                cache.firstAvailableLine +
                    layout.offsetEnd(
                        scrollRows,
                        scrollRows + containerHeight / lineHeight + OVERSCAN,
                    ),
            ),
        ),
    );

    // Live-region overlay rows, rendered in place below the committed lines.
    let overlayRows = $derived(cache.overlayRows);

    let totalHeight = $derived.by(() => {
        const linesHeight = layout.totalRows * lineHeight + truncationBannerHeight;
        const overlayHeight = overlayRows.length * lineHeight;
        // Reserve a row for the streaming cursor only when it stands on its own
        // fresh line. With a live tail it rides the last overlay row (counted in
        // overlayHeight), so no extra row is needed.
        const streamingHeight = isStreaming && overlayRows.length === 0 ? lineHeight : 0;
        // A finished log gets an "end of output" sentinel where the streaming
        // indicator sits while live (mutually exclusive). It's drawn two rows
        // tall so the dashed rule has breathing room above the centred label.
        const sentinelHeight = cache.finished && cache.totalLines > 0 ? lineHeight * 2 : 0;
        const blankHeight = cache.totalLines > 0 ? BLANK_LINES_AT_END * lineHeight : 0;
        return Math.max(
            linesHeight +
                overlayHeight +
                streamingHeight +
                sentinelHeight +
                blankHeight +
                frames.blockHeight,
            containerHeight,
        );
    });

    // Width of the virtual scroll surface: wide enough for the longest line
    // seen so far, never narrower than the viewport (so short logs show no
    // horizontal scrollbar). Wrapping pins the surface to the viewport, there
    // is no horizontal axis to scroll.
    let surfaceWidth = $derived(
        wrap
            ? containerWidth
            : Math.max(
                  containerWidth,
                  gutterWidth + cache.maxLineColumns * charWidth + SURFACE_PADDING,
              ),
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
        fetcher = new LogFetcher(cache, fn, (min, max) => {
            layout.measureRange(min, max);
            if (followTail) requestAnimationFrame(scrollToBottom);
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

    // Prune stale cache entries, separate from reads to avoid a read-write cycle on SvelteMap.
    $effect(() => {
        const vs = visibleStart;
        const ve = visibleEnd;
        untrack(() => cache.prune(vs, ve));
    });

    let prevTailRows = 0; // plain variable, intentionally non-reactive

    $effect(() => {
        // Track committed lines AND live overlay rows so an animating region at
        // the tail keeps a bottom-anchored viewport pinned to the bottom.
        const currentTail = cache.totalLines + overlayRows.length;
        if (currentTail > prevTailRows && followTail) requestAnimationFrame(scrollToBottom);
        prevTailRows = currentTail;
    });

    function onScroll(e: Event & { currentTarget: EventTarget & HTMLDivElement }) {
        const target = e.currentTarget;
        scrollTop = target.scrollTop;

        const distanceFromBottom = target.scrollHeight - target.scrollTop - target.clientHeight;
        followTail = distanceFromBottom < lineHeight * 2;
    }

    function scrollToBottom() {
        if (containerEl) containerEl.scrollTop = containerEl.scrollHeight;
    }

    function enableAutoScroll() {
        followTail = true;
        scrollToBottom();
    }

    function onResize() {
        if (containerEl) {
            containerHeight = containerEl.clientHeight;
            containerWidth = containerEl.clientWidth;
            // Resync the reactive scroll position with the DOM. The browser
            // clamps scrollTop when the viewport grows (e.g. maximizing the
            // console), but no scroll event fires for that clamp, so without
            // this the virtualizer keeps a stale, too-large scrollTop and
            // renders an empty window until the first manual scroll.
            scrollTop = containerEl.scrollTop;
            if (followTail) scrollToBottom();
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
        const merged = cache.applyEvent(event);
        if (merged.touched) layout.measureRange(merged.min, merged.max);

        if (cache.totalLines > prevTotal && followTail) requestAnimationFrame(scrollToBottom);

        if (fetcher) {
            fetcher.maybeRequestMissing(visibleStart, visibleEnd);
        }
    }

    export function reset() {
        cache.reset();
        frames.collapse();
        followTail = true;
        scrollTop = 0;
    }

    // Search-hit deep-link: scroll the viewport so the highlighted line is
    // centred, once per highlightLine, and pulse a one-shot flash class for
    // ~1.5s. createHighlightScroll keeps later layout changes (new lines on a
    // live run, resizes) from pulling the view back to the hit.
    const syncHighlightScroll = createHighlightScroll({
        line: () => highlightLine,
        ready: (line) => containerEl !== null && containerHeight > 0 && line < cache.totalLines,
        reveal: (line) => {
            if (!containerEl) return;
            const targetY = lineTop(line) - containerHeight / 2 + lineHeight / 2;
            const clamped = Math.max(0, Math.min(targetY, totalHeight - containerHeight));
            containerEl.scrollTo({ top: clamped, behavior: "smooth" });
            followTail = false;
            flashLine = line;
            if (flashTimer !== null) clearTimeout(flashTimer);
            flashTimer = setTimeout(() => {
                flashLine = null;
                flashTimer = null;
            }, 1500);
        },
        clear: () => {
            flashLine = null;
        },
    });
    $effect(syncHighlightScroll);

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

<div
    class="log-console relative flex h-full w-full flex-col overflow-hidden bg-[var(--rw-con-bg)] font-mono text-[12.5px] {className}"
>
    <!-- Hidden ruler: one monospace column is measured from this off-screen
         sample to size the horizontal scroll surface without per-line reflow. -->
    <span
        bind:this={rulerEl}
        aria-hidden="true"
        class="pointer-events-none absolute -top-[9999px] left-0 whitespace-pre select-none"
        >{RULER_SAMPLE}</span
    >

    {#if error}
        <div
            class="flex shrink-0 items-center gap-2 border-b border-[var(--rw-con-gutter)] px-3.5 py-2 text-[11.5px]"
            style="color: var(--rw-term-bad); background-color: color-mix(in srgb, var(--rw-term-bad) 12%, transparent);"
        >
            Failed to load log: {error}
        </div>
    {/if}

    <div bind:this={containerEl} class="flex-1 overflow-auto" onscroll={onScroll}>
        <div
            class="relative"
            style="height: {totalHeight}px; min-height: 100%; width: {surfaceWidth}px; min-width: 100%;"
        >
            {#if cache.firstAvailableLine > 0}
                <div
                    class="absolute right-0 left-0 flex items-center text-term-warn"
                    style="top: 0px; height: {lineHeight}px; background-color: color-mix(in srgb, var(--rw-term-warn) 15%, transparent);"
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
                    class="log-line absolute right-0 left-0 flex {wrap
                        ? 'items-start'
                        : 'items-center'} {flashLine === line.num ? 'log-line--flash' : ''}"
                    style="top: {lineTop(line.num)}px; height: {lineHeightPx(line.num)}px;"
                >
                    <div
                        class="sticky left-0 z-10 flex flex-shrink-0 items-center justify-end gap-1 bg-[var(--rw-con-bg)] pr-3 text-right text-[var(--rw-con-gutter)] select-none"
                        style="width: {gutterWidth}px;"
                    >
                        {#if isAnchor}
                            <button
                                type="button"
                                class="frame-toggle {frames.line === line.num
                                    ? 'text-aurora-400'
                                    : 'text-[var(--rw-con-dim)] hover:text-aurora-400'}"
                                title="{line.frameCount} earlier frame{line.frameCount === 1
                                    ? ''
                                    : 's'}, click to {frames.line === line.num ? 'hide' : 'rewind'}"
                                aria-label="Toggle frame history for line {line.num + 1}"
                                aria-expanded={frames.line === line.num}
                                onclick={() => frames.toggle(line.num)}
                            >
                                ↻
                            </button>
                        {/if}
                        {line.num + 1}
                    </div>
                    <div class="min-w-0 flex-1 pr-4 text-[var(--rw-con-text)]">
                        {#if line.text !== undefined}
                            <!-- eslint-disable-next-line svelte/no-at-html-tags -->
                            <span class={lineTextClass}>{@html ansiLineToHtml(line.text)}</span>
                        {:else}
                            <span class="text-[var(--rw-con-dim)] italic">Loading...</span>
                        {/if}
                    </div>
                </div>
            {/each}

            {#if frames.line !== null}
                <FrameHistoryBlock
                    history={frames}
                    top={lineTop(frames.line) + lineHeightPx(frames.line)}
                    {gutterWidth}
                />
            {/if}

            {#each overlayRows as row, i (i)}
                <div
                    class="log-line log-line--live absolute right-0 left-0 flex items-center"
                    style="top: {lineTop(cache.totalLines + i)}px; height: {lineHeight}px;"
                >
                    <div
                        class="sticky left-0 z-10 flex-shrink-0 bg-[var(--rw-con-bg)] pr-3 text-right text-[var(--rw-con-gutter)] select-none"
                        style="width: {gutterWidth}px;"
                    >
                        ⋯
                    </div>
                    <!-- The cursor rides the end of the last live overlay row so a
                         still-being-appended line shows the caret inline, the way a
                         terminal parks it before the next byte arrives. -->
                    <div class="flex-1 pr-4 text-[var(--rw-con-text)]">
                        <AnsiLine
                            text={row}
                        />{#if !cache.finished && i === overlayRows.length - 1}<span
                                class="stream-cursor"
                                aria-hidden="true"
                            ></span>{/if}
                    </div>
                </div>
            {/each}

            <!-- Streaming cursor on a fresh line: shown only when the last
                 committed line ended with a newline (no live tail). When output
                 is still being appended mid-line, the cursor rides the end of the
                 last overlay row instead (see the overlay loop above). -->
            {#if isStreaming && overlayRows.length === 0}
                <div
                    class="streaming-indicator absolute right-0 left-0 flex items-center"
                    style="top: {lineTop(cache.totalLines)}px; height: {lineHeight}px;"
                >
                    <div
                        class="sticky left-0 z-10 flex-shrink-0 bg-[var(--rw-con-bg)] pr-3 text-right text-[var(--rw-con-gutter)] select-none"
                        style="width: {gutterWidth}px;"
                    >
                        {cache.totalLines + 1}
                    </div>
                    <div class="flex-1 pr-4">
                        <span class="stream-cursor" aria-hidden="true"></span>
                    </div>
                </div>
            {/if}

            {#if cache.finished && cache.totalLines > 0}
                <div
                    class="sentinel absolute right-4 left-4 flex justify-center select-none"
                    style="top: {lineTop(cache.totalLines)}px; height: {lineHeight * 2}px;"
                >
                    <span
                        class="mt-[14px] border-t border-dashed pt-3 text-center text-[11px] tracking-wide {endTone ===
                        'warn'
                            ? 'sentinel--warn'
                            : 'sentinel--muted'}"
                    >
                        ── {endLabel} ──
                    </span>
                </div>
            {/if}

            {#if cache.totalLines > 0}
                {#each Array.from({ length: BLANK_LINES_AT_END }, (_, i) => i) as i (i)}
                    <div
                        class="absolute right-0 left-0 flex items-center"
                        style="top: {lineTop(
                            cache.finished
                                ? cache.totalLines + 2 + i
                                : cache.totalLines +
                                      overlayRows.length +
                                      (isStreaming && overlayRows.length === 0 ? 1 : 0) +
                                      i,
                        )}px; height: {lineHeight}px;"
                    >
                        <div
                            class="sticky left-0 z-10 flex-shrink-0 bg-[var(--rw-con-bg)] pr-3 text-right opacity-0 select-none"
                            style="width: {gutterWidth}px;"
                        >
                            ~
                        </div>
                    </div>
                {/each}
            {/if}

            {#if cache.totalLines === 0 && overlayRows.length === 0 && !loading && !fetcher?.isFetching}
                <div
                    class="absolute inset-0 flex items-center justify-center text-[var(--rw-con-dim)]"
                >
                    <div class="text-center">
                        <div class="mb-1 text-lg">No output yet</div>
                        <div class="text-sm text-[var(--rw-con-gutter)]">Waiting for logs...</div>
                    </div>
                </div>
            {/if}

            {#if cache.totalLines === 0 && (loading || fetcher?.isFetching)}
                <div
                    class="absolute inset-0 flex items-center justify-center text-[var(--rw-con-dim)]"
                >
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

    <LogConsoleFooter
        {followTail}
        streaming={isStreaming}
        finished={cache.finished}
        fetching={fetcher?.isFetching ?? false}
        totalLines={cache.totalLines}
        totalBytes={cache.totalBytes}
        onFollow={enableAutoScroll}
    />
</div>

<style>
    .log-console {
        tab-size: 8;
    }

    /* End-of-output marker: a centred label sitting under a dashed rule, the
       way the approved design closes a finished capture. */
    .sentinel--muted {
        color: var(--rw-con-gutter);
        border-top-color: var(--rw-con-gutter);
    }
    .sentinel--warn {
        color: var(--rw-term-warn);
        border-top-color: color-mix(in srgb, var(--rw-term-warn) 30%, transparent);
    }

    /* Subtle row hover on the fixed-dark console surface, a faint lift of the
       console text colour, never a theme-flipping fill. */
    .log-line:hover {
        background-color: color-mix(in srgb, var(--rw-con-text) 4%, transparent);
    }

    .log-line--flash {
        animation: log-line-flash 1.5s;
    }

    .frame-toggle {
        cursor: pointer;
        font-size: 0.85em;
        line-height: 1;
        background: transparent;
        border: none;
        padding: 0;
    }

    @keyframes log-line-flash {
        0% {
            background-color: color-mix(in srgb, var(--rw-term-warn) 45%, transparent);
        }
        100% {
            background-color: transparent;
        }
    }

    /* Blinking teal block caret shown while a run is actively streaming. One
       monospace cell wide so it reads as a terminal cursor parked at the write
       position, inline after a still-appending line, or alone on the next. */
    .stream-cursor {
        display: inline-block;
        width: 1ch;
        height: 1.1em;
        vertical-align: text-bottom;
        background-color: var(--color-aurora-400);
        animation: stream-cursor-blink 1.05s steps(2, start) infinite;
    }

    @keyframes stream-cursor-blink {
        0%,
        50% {
            opacity: 1;
        }
        50.01%,
        100% {
            opacity: 0;
        }
    }

    /* Reduced motion: keep the caret solid (still a clear "live" marker) rather
       than blinking. */
    @media (prefers-reduced-motion: reduce) {
        .stream-cursor {
            animation: none;
        }
    }

    .dot {
        display: inline-block;
        width: 4px;
        height: 4px;
        border-radius: 50%;
        background-color: currentColor;
        animation: dot-pulse 1s infinite;
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
