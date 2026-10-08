<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { untrack } from "svelte";
    import { Maximize2, Minimize2, Terminal as TerminalIcon, TextWrap } from "@lucide/svelte";
    import Kbd from "../Kbd.svelte";
    import LogConsole from "../LogConsole.svelte";
    import { portal } from "../../actions/portal.js";
    import { isLogEvent, type LogEvent, type LogSlice } from "../../log-console/types.js";
    import { extractErrorMessage } from "../../utils/error.js";
    import { isTypingTarget } from "../../utils/typing-target.js";
    import { displayStatus, type Run } from "@runwisp/common";
    import { RUN_STATUS_CONFIG } from "./status-config.js";
    import { runEndMarker } from "./run-helpers.js";

    let {
        run,
        suffix,
        fetchLogs,
        streamLogs,
        fetchLineHistory,
        highlightLine,
    }: {
        run: Run;
        /** Instance suffix of a multi-instance service, shown in focus mode. */
        suffix: string;
        fetchLogs: (
            runId: string,
            from: number,
            to: number,
        ) => Promise<LogSlice | LogEvent | undefined> | LogSlice | LogEvent | undefined;
        streamLogs:
            | ((
                  runId: string,
                  onEvent: (event: LogEvent) => void,
                  initialState?: { fromLine: number },
              ) => () => void)
            | undefined;
        fetchLineHistory: ((runId: string, lineNum: number) => Promise<string[][]>) | undefined;
        highlightLine: number | null;
    } = $props();

    const TAIL_LINES = 1000;

    let status = $derived(displayStatus(run.status, run.endReason));
    let config = $derived(RUN_STATUS_CONFIG[status]);
    let endMarker = $derived(runEndMarker(status));

    let logConsole = $state<{ onStream: (event: LogEvent) => void } | null>(null);

    // Message from the most recent seed-fetch failure (session expiry, daemon
    // restart mid-request, ...), surfaced to LogConsole instead of letting a
    // failed fetch render as an indistinguishable blank "no output" pane.
    let logFetchError = $state<string | null>(null);

    // Derive a stable scalar so the effect only re-runs when the id actually
    // changes, not on every run object reference swap from SSE array updates.
    let runId = $derived(run.id);

    // The run whose seed fetch has settled. Until it matches the shown run the
    // console says it's loading, not "No output yet".
    let seededRunId = $state<string | null>(null);

    // Fetches the batched tail page a run's console opens on (see the effect
    // below) and paints it into the console. Resolves the line the live
    // stream should resume from, or null when there's nothing left to stream
    // (the run already ended, or the caller cancelled while we were waiting).
    // A failed fetch still falls back to an SSE tail backfill from scratch,
    // it does not stop the console from live-tailing, but records the
    // failure so LogConsole can show it instead of looking like a quiet run.
    async function seedConsoleTail(
        id: string,
        isCancelled: () => boolean,
    ): Promise<{ fromLine: number } | null> {
        try {
            const seed = await fetchLogs(id, -TAIL_LINES, -1);
            if (isCancelled()) return null;
            if (!isLogEvent(seed)) return { fromLine: -TAIL_LINES };
            if (logConsole) logConsole.onStream(seed);
            return seed.finished // ended run: nothing live to follow
                ? null
                : { fromLine: seed.sizeLines > 0 ? seed.sizeLines : -TAIL_LINES };
        } catch (err) {
            if (isCancelled()) return null;
            logFetchError = extractErrorMessage(err, "Failed to load logs");
            return { fromLine: -TAIL_LINES };
        }
    }

    $effect(() => {
        const id = runId;

        const stream = streamLogs;
        if (!stream) {
            seededRunId = id;
            return;
        }

        logFetchError = null;

        let cleanup: (() => void) | undefined;
        let cancelled = false;

        untrack(() => {
            void (async () => {
                // Paint the tail in one batched page so the console opens
                // already at the newest output, instead of replaying 1000
                // backfill lines over SSE and visibly scrolling down to reach
                // the end. The live stream then picks up after the seeded tail
                // (its disk backfill from that anchor closes the fetch↔stream
                // race, and the daemon dedupes anything already shown).
                const seeded = await seedConsoleTail(id, () => cancelled);
                if (cancelled) return;
                seededRunId = id;
                if (!seeded) return;
                cleanup = stream(
                    id,
                    (event: LogEvent) => {
                        if (logConsole) logConsole.onStream(event);
                    },
                    { fromLine: seeded.fromLine },
                );
            })();
        });

        return () => {
            cancelled = true;
            if (cleanup) cleanup();
        };
    });

    // Maximize the console to a full-bleed overlay. The console wrapper is
    // never remounted, `maximizePortal` relocates the *live* node to <body>
    // (escaping any transformed/blurred ancestor that would otherwise trap a
    // position:fixed child) and moves it back on restore, so the SSE stream,
    // scroll position, and the {#key run.id} mount all survive the toggle.
    let consoleMaximized = $state(false);
    // Wrap long console lines instead of horizontally scrolling them. Owned
    // here so the toolbar toggle and the <LogConsole> share one source of truth.
    let consoleWrap = $state(false);

    function handleKeydown(event: KeyboardEvent) {
        if (event.key === "Escape" && consoleMaximized) {
            consoleMaximized = false;
            return;
        }
        // `F` toggles focus mode, the way a media player does, but never while
        // the operator is typing into a field (search box, etc.).
        if (
            (event.key === "f" || event.key === "F") &&
            !event.metaKey &&
            !event.ctrlKey &&
            !event.altKey
        ) {
            if (isTypingTarget(event.target)) return;
            event.preventDefault();
            consoleMaximized = !consoleMaximized;
        }
    }

    function maximizePortal(node: HTMLElement, active: boolean) {
        const anchor = document.createComment("maximized-console");
        function moveOut() {
            const parent = node.parentNode;
            if (parent && parent !== document.body) {
                parent.insertBefore(anchor, node);
                document.body.appendChild(node);
            }
        }
        function moveBack() {
            const parent = anchor.parentNode;
            if (parent) {
                parent.insertBefore(node, anchor);
                anchor.remove();
            }
        }
        if (active) moveOut();
        return {
            update(next: boolean) {
                if (next) moveOut();
                else moveBack();
            },
            destroy() {
                // Mirror the `portal` action: if still parked on <body>, remove
                // the node so Svelte's own teardown doesn't leak it there.
                if (node.parentNode === document.body) node.remove();
                anchor.remove();
            },
        };
    }
</script>

<svelte:window onkeydown={handleKeydown} />

<!-- Console hero, maximizes to a full-bleed overlay via portal -->
{#if consoleMaximized}
    <div
        use:portal
        class="console-backdrop fixed inset-0 z-40 bg-backdrop backdrop-blur-sm"
        onclick={() => (consoleMaximized = false)}
        role="presentation"
    ></div>
{/if}
<div
    use:maximizePortal={consoleMaximized}
    class={consoleMaximized
        ? "console-zoom fixed inset-3 z-50 flex flex-col overflow-hidden rounded-[4px] bg-[var(--rw-con-bg)] shadow-2xl ring-1 ring-[var(--rw-con-gutter)] sm:inset-6"
        : "ml-1 flex min-h-[300px] flex-1 flex-col overflow-hidden border-t border-[var(--rw-con-gutter)] bg-[var(--rw-con-bg)]"}
>
    <div
        class="@container flex shrink-0 items-center justify-between gap-3 border-b border-[var(--rw-con-gutter)] bg-[var(--rw-con-panel)] px-3.5 py-[9px] font-mono text-[11.5px] text-[var(--rw-con-dim)]"
    >
        <div class="flex min-w-0 items-center gap-3">
            {#if consoleMaximized}
                <!-- Focus-mode identity: which run is filling the screen -->
                <span class="flex min-w-0 items-center gap-2">
                    <span class="h-2 w-2 shrink-0 rounded-full {config.solidDot}"></span>
                    <span class="truncate font-medium text-[var(--rw-con-text)]">
                        {run.taskName}{suffix}
                    </span>
                    <span class="text-[var(--rw-con-gutter)]">·</span>
                    <span class="font-semibold capitalize {config.color}">{status}</span>
                </span>
            {:else}
                <span
                    class="flex items-center gap-2 font-semibold whitespace-nowrap text-[var(--rw-con-text)]"
                >
                    <TerminalIcon size={14} class="opacity-70" />
                    Console output
                </span>
                <span class="hidden text-[var(--rw-con-gutter)] @lg:inline">stdout + stderr</span>
            {/if}
        </div>
        <div class="flex shrink-0 items-center gap-3">
            <button
                type="button"
                onclick={() => (consoleWrap = !consoleWrap)}
                class="flex cursor-pointer items-center gap-1.5 rounded-[3px] px-2 py-1 hover:bg-[var(--rw-con-gutter)]/30 hover:text-[var(--rw-con-text)] {consoleWrap
                    ? 'text-[var(--rw-con-text)]'
                    : 'text-[var(--rw-con-dim)]'}"
                title={consoleWrap ? "Unwrap lines" : "Wrap long lines"}
                aria-pressed={consoleWrap}
                aria-label="Toggle line wrapping"
            >
                <TextWrap size={13} />
                <span class="hidden @sm:inline">Wrap</span>
            </button>
            <button
                type="button"
                onclick={() => (consoleMaximized = !consoleMaximized)}
                class="flex cursor-pointer items-center gap-1.5 rounded-[3px] px-2 py-1 text-[var(--rw-con-dim)] hover:bg-[var(--rw-con-gutter)]/30 hover:text-[var(--rw-con-text)]"
                title={consoleMaximized ? "Restore console (Esc)" : "Maximize console · F"}
                aria-label={consoleMaximized ? "Restore console" : "Maximize console"}
            >
                {#if consoleMaximized}
                    <Minimize2 size={13} />
                    <Kbd keys="Esc" size="xs" tone="console" />
                {:else}
                    <Maximize2 size={13} />
                    Expand
                {/if}
            </button>
        </div>
    </div>
    {#key run.id}
        <LogConsole
            bind:this={logConsole}
            fetchLogs={(f: number, t: number) => fetchLogs(run.id, f, t)}
            fetchLineHistory={fetchLineHistory
                ? (n: number) => fetchLineHistory(run.id, n)
                : undefined}
            bind:wrap={consoleWrap}
            class="min-h-0 flex-1"
            {highlightLine}
            endLabel={endMarker.label}
            endTone={endMarker.tone}
            error={logFetchError}
            loading={seededRunId !== run.id}
        />
    {/key}
</div>

<style>
    /* Gentle fade for the maximize backdrop; disabled under reduced motion. */
    .console-backdrop {
        animation: console-backdrop-in 150ms;
    }
    @keyframes console-backdrop-in {
        from {
            opacity: 0;
        }
        to {
            opacity: 1;
        }
    }

    /* The console zooms up as it takes over the viewport. */
    .console-zoom {
        animation: console-zoom-in 260ms cubic-bezier(0.33, 0.05, 0.2, 1);
    }
    @keyframes console-zoom-in {
        from {
            opacity: 0.4;
            transform: scale(0.96);
        }
        to {
            opacity: 1;
            transform: none;
        }
    }

    @media (prefers-reduced-motion: reduce) {
        .console-backdrop,
        .console-zoom {
            animation: none;
        }
    }
</style>
