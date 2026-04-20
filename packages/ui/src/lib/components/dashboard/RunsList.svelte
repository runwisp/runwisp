<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import {
        Clock,
        Timer,
        ChevronLeft,
        ChevronRight,
        ArrowUpDown,
        Search,
        Funnel,
    } from "@lucide/svelte";
    import Button from "../Button.svelte";
    import Badge from "../Badge.svelte";
    import type { Run } from "./types.js";
    import type { RunStatus } from "@runwisp/common";
    import { getRunStatusConfig, runDisplayStatus } from "./status-config.js";
    import { runDuration } from "./run-helpers.js";
    import { formatRelativeTime } from "../../utils/format.js";
    import { formatShortId } from "../../utils/id.js";

    let {
        runs,
        selectedRunId = $bindable(null),
        onselect,
        showFilters = false,
        showTaskName = false,
        headerLabel = "Run History",
        emptyText = "No runs yet",
    }: {
        runs: Run[];
        selectedRunId?: string | null;
        onselect?: (runId: string) => void;
        showFilters?: boolean;
        showTaskName?: boolean;
        headerLabel?: string;
        emptyText?: string;
    } = $props();

    function selectRun(runId: string) {
        selectedRunId = runId;
        onselect?.(runId);
    }

    let searchQuery = $state("");
    let statusFilter = $state<RunStatus | "all">("all");
    let sortDirection = $state<"asc" | "desc">("desc");
    let currentPage = $state(1);
    const pageSize = 10;

    let filteredRuns = $derived(
        showFilters
            ? runs.filter((r: Run) => {
                  if (statusFilter !== "all" && runDisplayStatus(r) !== statusFilter) return false;
                  if (searchQuery.trim()) {
                      const q = searchQuery.toLowerCase();
                      return (
                          r.id.toLowerCase().includes(q) || r.task_name.toLowerCase().includes(q)
                      );
                  }
                  return true;
              })
            : runs,
    );

    let sortedRuns = $derived(
        [...filteredRuns].sort((a: Run, b: Run) => {
            const dateA = new Date(a.created_at).getTime();
            const dateB = new Date(b.created_at).getTime();
            return sortDirection === "desc" ? dateB - dateA : dateA - dateB;
        }),
    );

    let totalPages = $derived(Math.max(1, Math.ceil(filteredRuns.length / pageSize)));
    let paginatedRuns = $derived(
        sortedRuns.slice((currentPage - 1) * pageSize, currentPage * pageSize),
    );
</script>

<div
    class="flex flex-col overflow-hidden rounded-xl border border-outline bg-surface-raised shadow-sm md:col-span-4 lg:col-span-3"
>
    {#if showFilters}
        <!-- Filter Header -->
        <div class="shrink-0 space-y-3 border-b border-outline-faint bg-surface-sunken p-3">
            <div class="flex items-center justify-between">
                <span class="text-xs font-semibold tracking-wider text-on-surface-muted uppercase"
                    >{headerLabel}</span
                >
                <Badge
                    variant="default"
                    size="sm"
                    class="bg-outline text-on-surface hover:bg-outline-faint"
                    >{filteredRuns.length}</Badge
                >
            </div>

            <div class="relative">
                <Search
                    class="absolute top-1/2 left-2.5 -translate-y-1/2 text-on-surface-faint"
                    size={14}
                />
                <input
                    type="text"
                    bind:value={searchQuery}
                    placeholder="Search task or ID..."
                    class="h-8 w-full rounded-md border border-outline bg-surface-raised pr-3 pl-8 text-sm transition-all placeholder:text-on-surface-faint focus:border-ring focus:ring-2 focus:ring-ring/20 focus:outline-none"
                />
            </div>

            <div class="flex items-center gap-2">
                <div class="relative flex-1">
                    <select
                        bind:value={statusFilter}
                        class="h-7 w-full appearance-none rounded-md border border-outline bg-surface-raised pr-6 pl-2 text-xs text-on-surface-muted focus:border-ring focus:outline-none"
                    >
                        <option value="all">All Statuses</option>
                        <option value="running">Running</option>
                        <option value="success">Success</option>
                        <option value="failed">Failed</option>
                        <option value="crashed">Crashed</option>
                    </select>
                    <Funnel
                        class="pointer-events-none absolute top-1/2 right-2 -translate-y-1/2 text-on-surface-faint"
                        size={12}
                    />
                </div>

                <Button
                    variant="ghost"
                    size="xs"
                    class="h-7 w-7 px-0"
                    onclick={() => (sortDirection = sortDirection === "desc" ? "asc" : "desc")}
                    title="Toggle Sort Order"
                >
                    {#snippet icon()}
                        <ArrowUpDown
                            size={14}
                            class="text-on-surface-muted {sortDirection === 'asc'
                                ? 'rotate-180'
                                : ''}"
                        />
                    {/snippet}
                </Button>
            </div>
        </div>
    {:else}
        <!-- Simple Header -->
        <div
            class="flex shrink-0 items-center justify-between border-b border-outline-faint bg-surface-sunken p-3"
        >
            <div class="flex items-center gap-2">
                <span class="text-xs font-semibold tracking-wider text-on-surface-muted uppercase"
                    >{headerLabel}</span
                >
                <Badge variant="default" size="sm">{runs.length}</Badge>
            </div>
            <div class="flex items-center gap-1">
                <Button
                    variant="ghost"
                    size="xs"
                    onclick={() => (sortDirection = sortDirection === "desc" ? "asc" : "desc")}
                    title="Sort by Date"
                >
                    {#snippet icon()}
                        <ArrowUpDown
                            size={14}
                            class="text-on-surface-muted {sortDirection === 'asc'
                                ? 'rotate-180'
                                : ''}"
                        />
                    {/snippet}
                </Button>
            </div>
        </div>
    {/if}

    <div class="min-h-0 flex-1 space-y-1 overflow-y-auto p-2">
        {#each paginatedRuns as run (run.id)}
            {@const isActive = selectedRunId === run.id}
            {@const config = getRunStatusConfig(runDisplayStatus(run))}
            {@const Icon = config.icon}
            {@const duration = runDuration(run)}
            <button
                class="group duration-normal relative w-full rounded-lg border p-3 text-left transition-all select-none
                    {isActive
                    ? 'border-primary-soft bg-primary-soft/50 shadow-sm'
                    : 'border-transparent bg-surface-raised hover:border-outline hover:bg-surface-sunken'}"
                onclick={() => selectRun(run.id)}
                onkeydown={(e) => e.key === "Enter" && selectRun(run.id)}
            >
                {#if showTaskName}
                    <!-- Runs variant: task name as primary, shortId secondary -->
                    <div class="mb-1 flex items-center justify-between">
                        <div class="flex min-w-0 items-center gap-2">
                            <div class="{config.color} shrink-0">
                                <Icon
                                    size={16}
                                    class={run.status === "running" ? "animate-spin" : ""}
                                />
                            </div>
                            <span class="truncate text-sm font-semibold text-on-surface">
                                {run.task_name}
                            </span>
                        </div>
                        <span
                            class="shrink-0 text-[10px] {isActive
                                ? 'font-medium text-primary'
                                : 'text-on-surface-faint'}"
                        >
                            {formatRelativeTime(run.start_at || run.created_at)}
                        </span>
                    </div>

                    <div class="flex items-center justify-between pl-6 text-xs">
                        <div
                            class="flex items-center gap-1 font-mono text-[11px] text-on-surface-muted"
                        >
                            #{formatShortId(run.id)}
                        </div>
                        {#if duration}
                            <div
                                class="flex items-center gap-1 text-on-surface-faint {isActive
                                    ? 'text-primary'
                                    : ''}"
                            >
                                <Timer size={10} />
                                <span class="font-mono">{duration}</span>
                            </div>
                        {/if}
                    </div>
                {:else}
                    <!-- Task variant: shortId as primary, status secondary -->
                    <div class="mb-1.5 flex items-center justify-between">
                        <div class="flex items-center gap-2">
                            <div class={config.color}>
                                <Icon
                                    size={16}
                                    class={run.status === "running" ? "animate-spin" : ""}
                                />
                            </div>
                            <span class="font-mono text-xs font-medium text-on-surface">
                                #{formatShortId(run.id)}
                            </span>
                        </div>
                        <span
                            class="text-[10px] {isActive
                                ? 'font-medium text-primary'
                                : 'text-on-surface-faint'}"
                        >
                            {formatRelativeTime(run.start_at || run.created_at)}
                        </span>
                    </div>

                    <div class="flex items-center justify-between text-xs">
                        <div class="flex items-center gap-2 text-on-surface-muted">
                            <span class="capitalize">{runDisplayStatus(run)}</span>
                        </div>
                        {#if duration}
                            <div
                                class="flex items-center gap-1 text-on-surface-faint {isActive
                                    ? 'text-primary'
                                    : ''}"
                            >
                                <Timer size={10} />
                                <span class="font-mono">{duration}</span>
                            </div>
                        {/if}
                    </div>
                {/if}

                {#if isActive}
                    <div
                        class="absolute top-1/2 left-0 h-8 w-1 -translate-y-1/2 rounded-r-full bg-primary"
                    ></div>
                {/if}
            </button>
        {/each}

        {#if paginatedRuns.length === 0}
            <div class="flex flex-col items-center gap-3 p-8 text-center text-on-surface-faint">
                <div class="rounded-full bg-surface-sunken p-3">
                    <Clock size={24} class="opacity-50" />
                </div>
                <span class="text-sm">{emptyText}</span>
            </div>
        {/if}
    </div>

    <!-- Pagination Footer -->
    <div
        class="flex shrink-0 items-center justify-between border-t border-outline-faint bg-surface-sunken p-2"
    >
        <Button
            variant="ghost"
            size="xs"
            disabled={currentPage === 1}
            onclick={() => currentPage--}
        >
            {#snippet icon()}<ChevronLeft size={14} />{/snippet}
        </Button>
        <span class="text-[10px] font-medium tracking-wide text-on-surface-muted uppercase">
            Page {currentPage} of {totalPages}
        </span>
        <Button
            variant="ghost"
            size="xs"
            disabled={currentPage === totalPages}
            onclick={() => currentPage++}
        >
            {#snippet icon()}<ChevronRight size={14} />{/snippet}
        </Button>
    </div>
</div>
