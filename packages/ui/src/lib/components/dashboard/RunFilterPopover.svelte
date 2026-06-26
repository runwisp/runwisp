<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<!--
  Runs filter, behind a single button + popover. On desktop it opens toward the
  run *content* (right of the list rail) so the list it filters stays visible
  and updates live; on phones it drops in as a full-width bottom sheet
  (mobileSheet). The button shows a count badge of active dimensions; the parent
  renders removable chips for each.

  Status is a five-bucket pick (Running / Succeeded / Failed / Skipped /
  Stopped) with an "Advanced" expander for the individual statuses — buckets are
  pure UI groupings over the same `statuses` array. Time is an independent
  From/To date pair; exit code is a free-form expression (137, >100, >100 <150)
  normalized to an inclusive range at the wire.

  All controls are NATIVE (<select>, <input>, checkbox, button) — a portalled
  Select component must not nest inside the portalled Popover, since clicking an
  option lands outside the popover's DOM subtree and triggers its outside-click
  close. Every mutation reassigns the whole `filters` object (never an in-place
  property write): the value reaches here through several levels of `bind:`, and
  only a fresh reference reliably re-triggers the parent's fetch effect.
-->
<script lang="ts">
    import { Funnel, ChevronRight } from "@lucide/svelte";
    import { END_REASONS, type RunStatus } from "@runwisp/common";
    import Popover from "../Popover.svelte";
    import Badge from "../Badge.svelte";
    import Toggle from "../Toggle.svelte";
    import { RUN_STATUS_CONFIG } from "./status-config.js";
    import {
        activeFilterCount,
        humanizeStatus,
        triggerDescription,
        FILTERABLE_TRIGGERS,
        STATUS_BUCKETS,
        bucketState,
        toggleBucket,
        isoToDayInput,
        dayStartIso,
        dayEndIso,
        isExitCodeExprValid,
        type RunsListFilters,
    } from "./run-filters.js";

    let {
        filters = $bindable(),
        showTask = false,
        tasks = [],
    }: {
        filters: RunsListFilters;
        showTask?: boolean;
        tasks?: { name: string }[];
    } = $props();

    // The individual statuses behind the "Advanced" expander, grouped the way
    // the buckets are summarized above.
    const STATUS_GROUPS: { label: string; values: RunStatus[] }[] = [
        { label: "Active", values: ["pending", "running"] },
        { label: "Outcomes", values: [...END_REASONS] },
    ];

    let advancedOpen = $state(false);

    const count = $derived(activeFilterCount(filters) + (showTask && filters.task_name ? 1 : 0));

    const headerClass = "text-xs font-semibold tracking-wide text-on-surface-muted uppercase";
    const labelClass = "text-2xs text-on-surface-faint uppercase";
    const selectClass =
        "h-9 w-full appearance-none rounded-md border border-outline bg-surface-raised px-2.5 text-sm text-on-surface focus:border-ring focus:outline-none";

    function statusDot(status: RunStatus): string {
        return RUN_STATUS_CONFIG[status].dot.replace(" animate-pulse", "");
    }

    // Reflect a tri-state bucket onto the native checkbox (no `indeterminate`
    // HTML attribute exists — it must be set on the DOM node).
    function indeterminate(node: HTMLInputElement, value: boolean) {
        node.indeterminate = value;
        return {
            update(v: boolean) {
                node.indeterminate = v;
            },
        };
    }

    function toggleStatus(value: string) {
        const statuses = filters.statuses.includes(value)
            ? filters.statuses.filter((s) => s !== value)
            : [...filters.statuses, value];
        filters = { ...filters, statuses };
    }

    // --- Time -------------------------------------------------------------
    //
    // Independent From/To date bounds. A bare date is that day's 00:00 (From) or
    // its end (To), so either edge applies on its own — From alone means
    // "everything since", To alone "everything before", and the same date in
    // both captures the whole day.

    function setFrom(value: string) {
        filters = { ...filters, created_after: value ? dayStartIso(value) : undefined };
    }

    function setTo(value: string) {
        filters = { ...filters, created_before: value ? dayEndIso(value) : undefined };
    }

    // --- Other dimensions -------------------------------------------------

    function setTask(value: string) {
        filters = { ...filters, task_name: value || undefined };
    }

    function setTrigger(value: string) {
        filters = { ...filters, triggered_by: value || undefined };
    }

    // Exit code: a free-form expression (`137`, `>100`, `>100 <150`) normalized
    // to an inclusive range at the wire. Edited in a local buffer for smooth
    // typing and committed on change; the popover content remounts on each open,
    // so the buffer re-seeds from `filters` every time it opens.
    let exitCodeInput = $state(filters.exit_code ?? "");
    const exitCodeValid = $derived(isExitCodeExprValid(exitCodeInput));

    function commitExitCode() {
        const trimmed = exitCodeInput.trim();
        filters = { ...filters, exit_code: trimmed || undefined };
    }

    function setRetriesOnly(checked: boolean) {
        filters = { ...filters, retries_only: checked ? true : undefined };
    }
</script>

<Popover placement="right-start" mobileSheet>
    {#snippet trigger()}
        <span
            class="inline-flex h-7 cursor-pointer items-center gap-1.5 rounded-md border px-2 text-xs font-medium transition-colors {count >
            0
                ? 'border-wisp-300 bg-primary-soft text-primary-soft-text'
                : 'border-transparent text-on-surface-muted hover:bg-surface-sunken'}"
            title="Filter runs"
        >
            <Funnel size={13} />
            Filter
            {#if count > 0}
                <Badge variant="primary" size="sm" class="px-1.5 py-0">{count}</Badge>
            {/if}
        </span>
    {/snippet}

    <div class="flex w-full flex-col gap-5 md:w-80">
        <!-- Status: five outcome buckets, with the individual statuses behind
             an Advanced expander for surgical filters. -->
        <section class="flex flex-col gap-2">
            <span class={headerClass}>Status</span>
            <div class="grid grid-cols-2 gap-x-3 gap-y-1.5">
                {#each STATUS_BUCKETS as bucket (bucket.key)}
                    {@const state = bucketState(filters.statuses, bucket)}
                    <label class="flex cursor-pointer items-center gap-2">
                        <input
                            type="checkbox"
                            checked={state === "on"}
                            use:indeterminate={state === "partial"}
                            onchange={() => (filters = toggleBucket(filters, bucket))}
                            class="size-4 shrink-0 cursor-pointer rounded border-outline accent-primary"
                        />
                        <span class="size-2.5 shrink-0 rounded-full {bucket.dot}"></span>
                        <span class="text-sm text-on-surface">{bucket.label}</span>
                    </label>
                {/each}
            </div>

            <details class="mt-0.5" bind:open={advancedOpen}>
                <summary
                    class="flex cursor-pointer items-center gap-1 text-2xs font-medium text-on-surface-muted transition-colors select-none marker:content-none hover:text-on-surface [&::-webkit-details-marker]:hidden"
                >
                    <ChevronRight size={12} class={advancedOpen ? "rotate-90" : ""} />
                    Advanced — pick exact statuses
                </summary>
                <div class="mt-2 flex flex-col gap-1.5">
                    {#each STATUS_GROUPS as group (group.label)}
                        <span class={labelClass}>{group.label}</span>
                        <div class="grid grid-cols-2 gap-x-2 gap-y-1">
                            {#each group.values as status (status)}
                                <label
                                    class="flex cursor-pointer items-center gap-1.5 truncate text-xs"
                                >
                                    <input
                                        type="checkbox"
                                        checked={filters.statuses.includes(status)}
                                        onchange={() => toggleStatus(status)}
                                        class="size-3.5 shrink-0 cursor-pointer rounded border-outline accent-primary"
                                    />
                                    <span class="size-2 shrink-0 rounded-full {statusDot(status)}"
                                    ></span>
                                    <span class="truncate text-on-surface">
                                        {humanizeStatus(status)}
                                    </span>
                                </label>
                            {/each}
                        </div>
                    {/each}
                </div>
            </details>
        </section>

        <!-- Date range (filters on created_at). Independent From/To bounds: a
             bare date is that day's 00:00 (From) or its end (To), so either edge
             applies on its own and the same date in both captures the day. -->
        <section class="flex flex-col gap-2">
            <span class={headerClass}>Date range</span>
            <div class="grid grid-cols-2 gap-2">
                <label class="flex flex-col gap-1">
                    <span class={labelClass}>From</span>
                    <input
                        type="date"
                        value={filters.created_after ? isoToDayInput(filters.created_after) : ""}
                        onchange={(e) => setFrom(e.currentTarget.value)}
                        class={selectClass}
                    />
                </label>
                <label class="flex flex-col gap-1">
                    <span class={labelClass}>To</span>
                    <input
                        type="date"
                        value={filters.created_before ? isoToDayInput(filters.created_before) : ""}
                        onchange={(e) => setTo(e.currentTarget.value)}
                        class={selectClass}
                    />
                </label>
            </div>
        </section>

        <!-- Task (cross-task /runs view only). -->
        {#if showTask}
            <section class="flex flex-col gap-2">
                <span class={headerClass}>Task</span>
                <select
                    class={selectClass}
                    value={filters.task_name ?? ""}
                    onchange={(e) => setTask(e.currentTarget.value)}
                >
                    <option value="">Any task</option>
                    {#each tasks as task (task.name)}
                        <option value={task.name}>{task.name}</option>
                    {/each}
                </select>
            </section>
        {/if}

        <!-- Triggered by. -->
        <section class="flex flex-col gap-2">
            <span class={headerClass}>Triggered by</span>
            <select
                class={selectClass}
                value={filters.triggered_by ?? ""}
                onchange={(e) => setTrigger(e.currentTarget.value)}
            >
                <option value="">Any trigger</option>
                {#each FILTERABLE_TRIGGERS as trigger (trigger)}
                    <option value={trigger}>{triggerDescription(trigger)}</option>
                {/each}
            </select>
        </section>

        <!-- Exit code: a free-form expression parsed to an inclusive range.
             Examples: 137 (exact), >100, <150, or >100 <150 (a window). -->
        <section class="flex flex-col gap-2">
            <span class={headerClass}>Exit code</span>
            <input
                type="text"
                inputmode="text"
                placeholder="e.g. 137, >100, >100 <150"
                bind:value={exitCodeInput}
                onchange={commitExitCode}
                aria-invalid={!exitCodeValid}
                class="{selectClass} {exitCodeValid ? '' : 'border-danger focus:border-danger'}"
            />
            <span class="text-2xs {exitCodeValid ? 'text-on-surface-faint' : 'text-danger'}">
                {exitCodeValid
                    ? "Match an exact code, or a range with > >= < <="
                    : "Use a number, optionally with > >= < <= (e.g. >100 <150)"}
            </span>
        </section>

        <!-- Retries. -->
        <section class="flex flex-col gap-2">
            <span class={headerClass}>Retries</span>
            <Toggle
                size="sm"
                checked={Boolean(filters.retries_only)}
                label="Only retried runs"
                onchange={setRetriesOnly}
            />
        </section>
    </div>
</Popover>
