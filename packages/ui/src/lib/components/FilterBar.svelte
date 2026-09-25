<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<!--
  One filter bar for every table. Driven by a declarative `FilterField[]` +
  a bound `values` object (see filter-spec.ts), the same way DataGrid is driven
  by `Column[]`.

  Progressive disclosure (Linear/Sentry-style): the row is a search box + a set
  of filter pills + a "+ Add filter" menu + Reset. `primary` fields are always
  shown as pills; every other field stays behind "+ Add filter" until the user
  adds it, then lives as a pill. Each pill is self-contained (label + value +
  clear in one element, see FilterPill) so activating a filter never adds a
  second row — the table below never jumps. Data-mode agnostic: the parent feeds
  `values` to applyFilters (client) or maps it to query params (server).
-->

<script lang="ts">
    import { SvelteSet } from "svelte/reactivity";
    import SearchInput from "./SearchInput.svelte";
    import FilterPill from "./FilterPill.svelte";
    import Popover from "./Popover.svelte";
    import { Plus, RotateCcw } from "@lucide/svelte";
    import {
        type FilterField,
        type FilterValues,
        fieldKeys,
        fieldLabel,
        isFieldActive,
    } from "./filter-spec.js";

    let {
        fields,
        values = $bindable({}),
        class: className = "",
    }: {
        fields: FilterField[];
        values?: FilterValues;
        class?: string;
    } = $props();

    const searchField = $derived(
        fields.find((f): f is Extract<FilterField, { type: "search" }> => f.type === "search"),
    );
    const pillFields = $derived(fields.filter((f) => f.type !== "search"));
    const isPrimary = (f: FilterField) => f.primary === true;

    // Non-primary fields surface as pills only once active or explicitly added.
    // `added` keeps a just-added-but-still-blank field visible while its editor
    // is open; it's dropped again if the user closes the editor without a value.
    const added = new SvelteSet<string>();
    const isShown = (f: FilterField) =>
        isPrimary(f) || isFieldActive(f, values) || added.has(f.key);
    const shownPills = $derived(pillFields.filter(isShown));
    const addable = $derived(pillFields.filter((f) => !isShown(f)));

    const anyActive = $derived(fields.some((f) => isFieldActive(f, values)));

    let justAdded = $state<string | null>(null);
    let addOpen = $state(false);
    let searchEl = $state<HTMLInputElement | null>(null);

    function addFilter(f: FilterField) {
        added.add(f.key);
        justAdded = f.key;
        addOpen = false;
    }

    function onPillClose(f: FilterField) {
        if (justAdded === f.key) justAdded = null;
        if (!isPrimary(f) && !isFieldActive(f, values)) added.delete(f.key);
    }

    function clearField(f: FilterField) {
        for (const k of fieldKeys(f)) values[k] = "";
    }

    function reset() {
        for (const f of fields) clearField(f);
        added.clear();
    }

    // `/` focuses the search box, unless the user is already typing somewhere.
    function onWindowKeydown(e: KeyboardEvent) {
        if (e.key !== "/" || !searchEl) return;
        const t = e.target;
        if (
            t instanceof HTMLElement &&
            (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.isContentEditable)
        )
            return;
        e.preventDefault();
        searchEl.focus();
    }
</script>

<svelte:window onkeydown={onWindowKeydown} />

<div class="flex flex-wrap items-center gap-2 {className}">
    {#if searchField}
        <SearchInput
            bind:value={values[searchField.key]}
            bind:element={searchEl}
            placeholder={searchField.placeholder ?? "Search..."}
        />
    {/if}

    {#each shownPills as field (field.key)}
        <FilterPill
            {field}
            bind:values
            autoOpen={justAdded === field.key}
            onClose={() => onPillClose(field)}
        />
    {/each}

    {#if addable.length > 0}
        <Popover bind:open={addOpen} placement="bottom-start" mobileSheet>
            {#snippet trigger()}
                <span
                    class="inline-flex cursor-pointer items-center gap-1.5 rounded-[3px] border border-dashed border-outline-hover bg-transparent px-3 py-2 text-sm text-on-surface-muted hover:border-solid hover:bg-surface-raised hover:text-on-surface"
                >
                    <Plus size={14} />
                    {shownPills.length > 0 ? "Filter" : "Add filter"}
                </span>
            {/snippet}
            <div class="flex w-48 flex-col gap-0.5">
                {#each addable as field (field.key)}
                    <button
                        type="button"
                        class="rounded-[3px] px-2.5 py-1.5 text-left text-sm text-on-surface-muted hover:bg-surface-sunken hover:text-on-surface"
                        onclick={(e) => {
                            // Stop the click reaching window: the pill we're about
                            // to add auto-opens its editor, and this same click would
                            // otherwise trip that fresh Popover's outside-click close.
                            e.stopPropagation();
                            addFilter(field);
                        }}
                    >
                        {fieldLabel(field)}
                    </button>
                {/each}
            </div>
        </Popover>
    {/if}

    {#if anyActive}
        <button
            class="inline-flex items-center gap-1.5 rounded-[3px] px-2.5 py-2 text-sm font-medium text-on-surface-muted hover:bg-surface-sunken hover:text-on-surface"
            onclick={reset}
        >
            <RotateCcw size={14} />
            Reset
        </button>
    {/if}
</div>
