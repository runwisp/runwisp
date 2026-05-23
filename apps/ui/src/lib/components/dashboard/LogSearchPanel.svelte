<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { Search, X, LoaderCircle } from "@lucide/svelte";
    import Button from "@runwisp/ui/components/Button.svelte";
    import { tasksApi } from "$lib/api";
    import type { LogSearchHit } from "$lib/logs";
    import { formatShortId } from "@runwisp/ui";

    let {
        taskName,
        onSelectHit,
    }: {
        taskName: string;
        onSelectHit: (hit: LogSearchHit) => void;
    } = $props();

    let q = $state("");
    let regex = $state(false);
    let caseSensitive = $state(false);
    let loading = $state(false);
    let hits = $state<LogSearchHit[]>([]);
    let scannedRuns = $state(0);
    let exhausted = $state(false);
    let errorMessage = $state("");

    async function search() {
        if (!q) return;
        loading = true;
        errorMessage = "";
        try {
            const result = await tasksApi.searchLogs(taskName, {
                q,
                regex,
                case: caseSensitive,
            });
            hits = result.hits;
            scannedRuns = result.scanned_runs;
            exhausted = result.exhausted;
        } catch (err) {
            errorMessage = err instanceof Error ? err.message : "Search failed";
            hits = [];
        } finally {
            loading = false;
        }
    }

    function onSubmit(e: SubmitEvent) {
        e.preventDefault();
        void search();
    }

    function clearResults() {
        hits = [];
        q = "";
        errorMessage = "";
    }
</script>

<div class="flex flex-col gap-3 rounded border border-slate-800 bg-slate-900/40 p-3">
    <form class="flex items-center gap-2" onsubmit={onSubmit}>
        <Search class="text-slate-500" size={16} />
        <input
            type="text"
            placeholder="Search log lines across runs…"
            bind:value={q}
            class="flex-1 rounded border border-slate-700 bg-slate-950 px-2 py-1 text-sm text-slate-100 placeholder:text-slate-500 focus:border-aurora-500 focus:outline-none"
        />
        <label class="flex items-center gap-1 text-xs text-slate-400">
            <input type="checkbox" bind:checked={regex} class="accent-aurora-500" />
            regex
        </label>
        <label class="flex items-center gap-1 text-xs text-slate-400">
            <input type="checkbox" bind:checked={caseSensitive} class="accent-aurora-500" />
            case
        </label>
        <Button type="submit" variant="primary" size="sm" disabled={loading || !q}>
            {#if loading}<LoaderCircle class="animate-spin" size={14} />{/if}
            Search
        </Button>
        {#if hits.length > 0 || errorMessage}
            <Button type="button" variant="ghost" size="sm" onclick={clearResults}>
                <X size={14} />
            </Button>
        {/if}
    </form>

    {#if errorMessage}
        <p class="text-xs text-danger-surface">{errorMessage}</p>
    {:else if loading}
        <p class="text-xs text-slate-500">Searching…</p>
    {:else if hits.length === 0 && q && !loading}
        <p class="text-xs text-slate-500">
            No matches across {scannedRuns} run{scannedRuns === 1 ? "" : "s"}.
        </p>
    {:else if hits.length > 0}
        <div class="flex flex-col gap-1 text-xs">
            <p class="text-slate-500">
                {hits.length} hit{hits.length === 1 ? "" : "s"} across {scannedRuns} run{scannedRuns ===
                1
                    ? ""
                    : "s"}{exhausted ? "" : " (more available — refine your query)"}
            </p>
            <ul class="max-h-72 overflow-y-auto rounded border border-slate-800 bg-slate-950">
                {#each hits as hit (hit.run_id + ":" + hit.n)}
                    <li>
                        <button
                            type="button"
                            class="flex w-full items-center gap-2 border-b border-slate-900 px-2 py-1 text-left hover:bg-slate-800/60"
                            onclick={() => onSelectHit(hit)}
                        >
                            <span class="font-mono text-slate-500">{formatShortId(hit.run_id)}</span
                            >
                            <span class="font-mono text-slate-600">L{hit.n}</span>
                            <span
                                class="truncate font-mono text-slate-300"
                                class:text-danger-surface={hit.stream === "stderr"}
                            >
                                {hit.text}
                            </span>
                        </button>
                    </li>
                {/each}
            </ul>
        </div>
    {/if}
</div>
