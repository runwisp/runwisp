<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { Search, LoaderCircle } from "@lucide/svelte";
    import Button from "@runwisp/ui/components/Button.svelte";
    import Modal from "@runwisp/ui/components/Modal.svelte";
    import Input from "@runwisp/ui/components/Input.svelte";
    import Checkbox from "@runwisp/ui/components/Checkbox.svelte";
    import { formatRelativeTimeWithAbsolute } from "@runwisp/ui";
    import { tasksApi } from "$lib/api";
    import type { LogSearchHit } from "$lib/logs";

    let {
        taskName,
        open = $bindable(false),
        onSelectHit,
    }: {
        taskName: string;
        open?: boolean;
        onSelectHit: (hit: LogSearchHit) => void;
    } = $props();

    let q = $state("");
    let regex = $state(false);
    let caseSensitive = $state(false);
    let loading = $state(false);
    let loadingMore = $state(false);
    let hits = $state<LogSearchHit[]>([]);
    let scannedRuns = $state(0);
    let exhausted = $state(false);
    let cursor = $state("");
    let errorMessage = $state("");
    let searched = $state(false);
    let searchSeq = 0;
    let sentinel = $state<HTMLLIElement | null>(null);

    interface HitGroup {
        runId: string;
        ts: number;
        hits: LogSearchHit[];
    }

    const groups = $derived.by<HitGroup[]>(() => {
        const out: HitGroup[] = [];
        let current: HitGroup | undefined;
        for (const hit of hits) {
            if (!current || current.runId !== hit.run_id) {
                current = { runId: hit.run_id, ts: hit.ts, hits: [hit] };
                out.push(current);
            } else {
                current.hits.push(hit);
            }
        }
        return out;
    });

    async function search() {
        if (!q) return;
        searchSeq += 1;
        const seq = searchSeq;
        loading = true;
        errorMessage = "";
        searched = true;
        hits = [];
        cursor = "";
        scannedRuns = 0;
        exhausted = false;
        try {
            const result = await tasksApi.searchLogs(taskName, {
                q,
                regex,
                case: caseSensitive,
            });
            if (seq !== searchSeq) return;
            hits = result.hits;
            scannedRuns = result.scanned_runs;
            exhausted = result.exhausted;
            cursor = result.next_cursor;
        } catch (err) {
            if (seq !== searchSeq) return;
            errorMessage = err instanceof Error ? err.message : "Search failed";
            hits = [];
        } finally {
            if (seq === searchSeq) loading = false;
        }
    }

    async function loadMore() {
        if (loadingMore || loading || exhausted || !cursor || !q) return;
        const seq = searchSeq;
        loadingMore = true;
        errorMessage = "";
        try {
            const result = await tasksApi.searchLogs(taskName, {
                q,
                regex,
                case: caseSensitive,
                cursor,
            });
            if (seq !== searchSeq) return;
            hits = [...hits, ...result.hits];
            scannedRuns += result.scanned_runs;
            exhausted = result.exhausted;
            cursor = result.next_cursor;
        } catch (err) {
            if (seq !== searchSeq) return;
            errorMessage = err instanceof Error ? err.message : "Failed to load more";
        } finally {
            if (seq === searchSeq) loadingMore = false;
        }
    }

    $effect(() => {
        if (!sentinel || exhausted) return;
        const observer = new IntersectionObserver(
            (entries) => {
                if (entries[0]?.isIntersecting) void loadMore();
            },
            { root: null, rootMargin: "200px" },
        );
        observer.observe(sentinel);
        return () => {
            observer.disconnect();
        };
    });

    function onSubmit(e: SubmitEvent) {
        e.preventDefault();
        void search();
    }

    function pickHit(hit: LogSearchHit) {
        onSelectHit(hit);
        open = false;
    }

    function pickGroup(group: HitGroup) {
        const first = group.hits[0];
        if (first) pickHit(first);
    }

    function hitCountLabel(n: number): string {
        return n === 1 ? "1 hit" : `${String(n)} hits`;
    }

    function runCountLabel(n: number): string {
        return n === 1 ? "1 run" : `${String(n)} runs`;
    }
</script>

<Modal bind:open size="full" title="Search Logs">
    <div class="flex flex-col gap-4">
        <form class="flex flex-col gap-3" onsubmit={onSubmit}>
            <Input size="sm" placeholder="Search log lines across runs…" bind:value={q} autofocus>
                {#snippet leadingIcon()}
                    <Search size={16} />
                {/snippet}
            </Input>
            <div class="flex flex-wrap items-center gap-x-5 gap-y-2">
                <Checkbox size="sm" label="Regex" bind:checked={regex} />
                <Checkbox size="sm" label="Case sensitive" bind:checked={caseSensitive} />
                <div class="ml-auto">
                    <Button type="submit" variant="primary" size="sm" disabled={loading || !q}>
                        {#snippet icon()}
                            {#if loading}
                                <LoaderCircle class="animate-spin" size={14} />
                            {:else}
                                <Search size={14} />
                            {/if}
                        {/snippet}
                        Search
                    </Button>
                </div>
            </div>
        </form>

        {#if loading}
            <p class="text-sm text-on-surface-muted">Searching…</p>
        {:else if searched && hits.length === 0 && !errorMessage}
            <p class="text-sm text-on-surface-muted">
                No matches across {runCountLabel(scannedRuns)}.
            </p>
        {:else if hits.length > 0}
            <p class="text-xs text-on-surface-muted">
                {hitCountLabel(hits.length)} across {runCountLabel(scannedRuns)}
            </p>
            <ul class="flex flex-col gap-3">
                {#each groups as group (group.runId)}
                    <li
                        class="overflow-hidden rounded-lg border border-outline-faint bg-surface-raised"
                    >
                        <button
                            type="button"
                            class="flex w-full items-center justify-between gap-3 border-b border-outline-faint bg-surface-sunken px-3 py-2 text-left transition-colors hover:bg-surface-sunken/70"
                            onclick={() => pickGroup(group)}
                        >
                            <span class="text-sm font-medium text-on-surface">
                                {formatRelativeTimeWithAbsolute(new Date(group.ts))}
                            </span>
                            <span class="text-xs text-on-surface-muted">
                                {hitCountLabel(group.hits.length)}
                            </span>
                        </button>
                        <ul>
                            {#each group.hits as hit (hit.n)}
                                <li>
                                    <button
                                        type="button"
                                        class="flex w-full items-baseline gap-3 px-3 py-1.5 text-left transition-colors hover:bg-surface-sunken/50"
                                        onclick={() => pickHit(hit)}
                                    >
                                        <span
                                            class="shrink-0 font-mono text-xs text-on-surface-faint"
                                            >L{hit.n}</span
                                        >
                                        <span
                                            class="truncate font-mono text-xs {hit.stream ===
                                            'stderr'
                                                ? 'text-danger-surface'
                                                : 'text-on-surface'}"
                                        >
                                            {hit.text}
                                        </span>
                                    </button>
                                </li>
                            {/each}
                        </ul>
                    </li>
                {/each}
                {#if loadingMore}
                    <li class="flex items-center gap-2 px-1 py-2 text-xs text-on-surface-muted">
                        <LoaderCircle class="animate-spin" size={14} />
                        Loading more…
                    </li>
                {:else if !exhausted}
                    <li bind:this={sentinel} aria-hidden="true"></li>
                {:else}
                    <li class="px-1 py-2 text-xs text-on-surface-faint">End of results</li>
                {/if}
            </ul>
            {#if errorMessage}
                <p class="text-sm text-danger-surface">{errorMessage}</p>
            {/if}
        {:else if errorMessage}
            <p class="text-sm text-danger-surface">{errorMessage}</p>
        {:else}
            <p class="text-sm text-on-surface-muted">
                Type a query and press Enter to search across recent runs.
            </p>
        {/if}
    </div>
</Modal>
