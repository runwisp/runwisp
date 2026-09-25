<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<!--
  One filter, one element. The pill is both the control and its own "chip":
  inactive it reads `Label ▾` (muted); active it reads `Label: value ✕` (accent)
  with an inline clear. Active vs inactive is a restyle of the SAME element — it
  never spawns a second row, so the table below never jumps. Clicking the body
  opens a type-specific editor popover (select menu / number / date range).
-->

<script lang="ts">
    import { untrack } from "svelte";
    import Popover from "./Popover.svelte";
    import Input from "./Input.svelte";
    import { Check, ChevronDown, X } from "@lucide/svelte";
    import {
        type FilterField,
        type FilterValues,
        fieldKeys,
        fieldLabel,
        isFieldActive,
        isBlank,
    } from "./filter-spec.js";

    let {
        field,
        values = $bindable({}),
        // Open the editor on mount — set when the pill was just added from the
        // "+ Add filter" menu, so the user lands straight in the value picker.
        autoOpen = false,
        onClose,
    }: {
        field: FilterField;
        values?: FilterValues;
        autoOpen?: boolean;
        onClose?: () => void;
    } = $props();

    // Capture the initial autoOpen once; later prop changes must not reopen it.
    let open = $state(untrack(() => autoOpen));
    const active = $derived(isFieldActive(field, values));
    const label = $derived(fieldLabel(field));

    // The active-state summary shown after the label (`Label: <this>`).
    const summary = $derived.by(() => {
        if (field.type === "select") {
            const v = values[field.key];
            return field.options.find((o) => String(o.value) === String(v))?.label ?? String(v);
        }
        if (field.type === "daterange") {
            const from = values[`${field.key}From`];
            const to = values[`${field.key}To`];
            return `${from || "…"} → ${to || "…"}`;
        }
        return values[field.key] ?? "";
    });

    function clear() {
        for (const k of fieldKeys(field)) values[k] = "";
    }

    // Tell the parent when the editor closes so an abandoned "just added" pill
    // (opened but left blank) can be dropped from the visible set.
    let wasOpen = false;
    $effect(() => {
        if (wasOpen && !open) onClose?.();
        wasOpen = open;
    });

    // Date-range quick presets. Browser runtime, so reading the clock is fine.
    const DAY_MS = 86_400_000;
    const isoDay = (ms: number) => new Date(ms).toISOString().slice(0, 10);
    function setRange(days: number) {
        if (field.type !== "daterange") return;
        const now = Date.now();
        values[`${field.key}From`] = isoDay(now - days * DAY_MS);
        values[`${field.key}To`] = isoDay(now);
    }
</script>

<!--
  Split pill: the body opens the editor (Popover trigger); the trailing ✕ is a
  separate sibling button that clears the field. Keeping ✕ outside the Popover
  trigger avoids nesting one interactive control inside another.
-->
<div
    class="inline-flex items-center rounded-[3px] border text-sm {active
        ? 'border-primary-soft-border bg-primary-soft text-primary-soft-text'
        : 'border-outline bg-surface-raised text-on-surface-muted hover:border-outline-hover hover:text-on-surface'}"
>
    <Popover bind:open placement="bottom-start" mobileSheet>
        {#snippet trigger()}
            <span class="inline-flex cursor-pointer items-center gap-1.5 px-3 py-2">
                <span class="truncate">
                    {label}{#if active}<span class="font-medium">: {summary}</span>{/if}
                </span>
                {#if !active}<ChevronDown size={14} class="opacity-60" />{/if}
            </span>
        {/snippet}

        {#if field.type === "select"}
            <div class="flex w-52 flex-col gap-0.5">
                {#each field.options as opt (String(opt.value))}
                    {@const selected = String(values[field.key] ?? "") === String(opt.value)}
                    <button
                        type="button"
                        class="flex items-center justify-between gap-2 rounded-[3px] px-2.5 py-1.5 text-left text-sm hover:bg-surface-sunken {selected
                            ? 'text-on-surface'
                            : 'text-on-surface-muted'}"
                        onclick={() => {
                            values[field.key] = String(opt.value);
                            open = false;
                        }}
                    >
                        <span class="truncate">{opt.label}</span>
                        {#if selected}<Check size={15} class="shrink-0 text-primary" />{/if}
                    </button>
                {/each}
            </div>
        {:else if field.type === "number"}
            <div class="w-44">
                <Input
                    type="number"
                    value={values[field.key] ?? ""}
                    placeholder={field.placeholder ?? field.label}
                    autofocus
                    onchange={(e) => (values[field.key] = e.currentTarget.value)}
                    onkeydown={(e) => {
                        if (e.key === "Enter") open = false;
                    }}
                />
            </div>
        {:else if field.type === "daterange"}
            <div class="flex w-64 flex-col gap-3">
                <div class="flex items-center gap-2">
                    <label class="flex-1">
                        <span class="mb-1 block text-xs text-on-surface-faint">From</span>
                        <Input type="date" bind:value={values[`${field.key}From`]} size="sm" />
                    </label>
                    <label class="flex-1">
                        <span class="mb-1 block text-xs text-on-surface-faint">To</span>
                        <Input type="date" bind:value={values[`${field.key}To`]} size="sm" />
                    </label>
                </div>
                <div class="flex flex-wrap gap-1.5">
                    {#each [{ label: "Today", days: 0 }, { label: "7 days", days: 7 }, { label: "30 days", days: 30 }] as p (p.days)}
                        <button
                            type="button"
                            class="rounded-[3px] border border-outline bg-surface-raised px-2 py-1 text-xs text-on-surface-muted hover:border-outline-hover hover:text-on-surface"
                            onclick={() => setRange(p.days)}
                        >
                            {p.label}
                        </button>
                    {/each}
                    {#if !isBlank(values[`${field.key}From`]) || !isBlank(values[`${field.key}To`])}
                        <button
                            type="button"
                            class="ml-auto rounded-[3px] px-2 py-1 text-xs text-on-surface-muted hover:text-on-surface"
                            onclick={() => clear()}
                        >
                            Clear
                        </button>
                    {/if}
                </div>
            </div>
        {/if}
    </Popover>

    {#if active}
        <button
            type="button"
            class="self-stretch rounded-r-[3px] border-l border-primary-soft-border px-2 hover:bg-primary/15"
            onclick={clear}
            aria-label="Clear {label} filter"
        >
            <X size={13} />
        </button>
    {/if}
</div>
