<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<!--
  Debounced search box for FilterBar. `value` is the committed term the parent
  filters on; keystrokes are debounced so client re-filtering / server refetch
  fires once the user pauses, not per character. `Esc` and the trailing ✕ clear
  immediately. Exposes its raw <input> via `element` so FilterBar's `/` shortcut
  can focus it.
-->

<script lang="ts">
    import Input from "./Input.svelte";
    import { Search, X } from "@lucide/svelte";
    import { untrack, onDestroy } from "svelte";
    import { debounce } from "../utils/debounce.js";

    let {
        value = $bindable(""),
        placeholder = "Search...",
        element = $bindable<HTMLInputElement | null>(null),
    }: {
        value?: string | undefined;
        placeholder?: string;
        element?: HTMLInputElement | null;
    } = $props();

    // `raw` is the live text; `value` is the debounced, committed term. Seeded
    // once from `value` so a URL-restored search survives mount undebounced.
    let raw = $state(value);
    let wrapperEl = $state<HTMLDivElement | null>(null);

    const commit = debounce((v: string) => (value = v), 250);
    onDestroy(() => commit.cancel());

    // Expose the real input for FilterBar's `/`-to-focus shortcut.
    $effect(() => {
        element = wrapperEl?.querySelector("input") ?? null;
    });

    // External writes to `value` (Reset / clear-all) win immediately: adopt them
    // and drop any pending debounce. Depends only on `value` (raw is read
    // untracked), so typing never re-enters here. Tiny race: a reset that leaves
    // `value` unchanged ("" -> "") can't cancel a first-keystroke debounce —
    // negligible at 250ms. ponytail: guarded two-way sync, fine for one field.
    $effect(() => {
        const v = value;
        untrack(() => {
            if (v !== raw) {
                commit.cancel();
                raw = v;
            }
        });
    });

    function clear() {
        commit.cancel();
        raw = "";
        value = "";
        wrapperEl?.querySelector("input")?.focus();
    }

    function onKeydown(e: KeyboardEvent) {
        if (e.key === "Escape" && raw) {
            e.preventDefault();
            clear();
        }
    }
</script>

<div bind:this={wrapperEl} class="relative w-full sm:w-64">
    <Input
        bind:value={raw}
        {placeholder}
        oninput={() => commit(raw)}
        onkeydown={onKeydown}
        style={raw ? "padding-right:2.25rem" : undefined}
    >
        {#snippet leadingIcon()}<Search size={16} />{/snippet}
    </Input>
    {#if raw}
        <button
            type="button"
            class="absolute top-1/2 right-2 -translate-y-1/2 rounded-[3px] p-1 text-on-surface-faint hover:bg-surface-sunken hover:text-on-surface"
            onclick={clear}
            aria-label="Clear search"
        >
            <X size={14} />
        </button>
    {/if}
</div>
