<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { Search, X, LoaderCircle } from "@lucide/svelte";
    import { browser } from "$app/environment";
    import { headerSearchStore } from "$lib/stores";

    // Debounce typing before handing the query to the page, so a filter or a
    // log search doesn't fire a request on every keystroke.
    const DEBOUNCE_MS = 250;

    let inputEl = $state<HTMLInputElement | null>(null);

    // ⌘ on Apple, Ctrl elsewhere — show the shortcut the operator actually presses.
    let isMac = $state(false);
    $effect(() => {
        if (browser) isMac = /mac|iphone|ipad|ipod/i.test(navigator.userAgent);
    });
    let shortcut = $derived(isMac ? "⌘K" : "Ctrl K");

    // Debounced dispatch to the registered page. Re-runs on every keystroke;
    // the cleanup cancels the pending fire, so only a settled query lands.
    $effect(() => {
        const spec = headerSearchStore.spec;
        if (!spec) return;
        const q = headerSearchStore.query;
        const id = setTimeout(() => spec.onSearch(q), DEBOUNCE_MS);
        return () => clearTimeout(id);
    });

    function onWindowKeydown(e: KeyboardEvent) {
        if (e.key.toLowerCase() !== "k" || !(e.metaKey || e.ctrlKey) || e.shiftKey || e.altKey) {
            return;
        }
        // Don't steal ⌘K while the operator is typing in some other field.
        const target = e.target;
        if (
            target !== inputEl &&
            (target instanceof HTMLInputElement ||
                target instanceof HTMLTextAreaElement ||
                (target instanceof HTMLElement && target.isContentEditable))
        ) {
            return;
        }
        e.preventDefault();
        inputEl?.focus();
        inputEl?.select();
    }

    function onInputKeydown(e: KeyboardEvent) {
        if (e.key !== "Escape") return;
        e.stopPropagation();
        if (headerSearchStore.query) {
            headerSearchStore.clear();
        } else {
            inputEl?.blur();
        }
    }
</script>

<svelte:window onkeydown={onWindowKeydown} />

{#if headerSearchStore.active && headerSearchStore.spec}
    <!-- Quiet at rest, lifts on focus: the whole pill is the focus surface —
         its border warms to the ring, a soft ring hugs it, it floats up off the
         bar, and the icon tints to primary. The input's own focus outline is
         suppressed (below) so the pill reads as one control, not two rings. -->
    <div
        class="group/search relative flex h-9 w-full max-w-md items-center gap-2.5 rounded-xl border border-outline bg-surface-sunken/60 px-3 text-sm shadow-sm transition-all duration-150 focus-within:border-ring focus-within:bg-surface-raised focus-within:shadow-md focus-within:ring-2 focus-within:ring-ring/35"
    >
        <Search
            size={15}
            class="shrink-0 text-on-surface-faint transition-colors group-focus-within/search:text-primary"
        />
        <input
            bind:this={inputEl}
            type="text"
            value={headerSearchStore.query}
            oninput={(e) => headerSearchStore.setQuery(e.currentTarget.value)}
            onkeydown={onInputKeydown}
            placeholder={headerSearchStore.spec.placeholder}
            autocomplete="off"
            spellcheck="false"
            aria-label={headerSearchStore.spec.placeholder}
            class="min-w-0 flex-1 border-0 bg-transparent text-on-surface outline-none! placeholder:text-on-surface-faint focus:[box-shadow:none]"
        />
        {#if headerSearchStore.loading}
            <LoaderCircle size={14} class="shrink-0 animate-spin text-on-surface-faint" />
        {/if}
        {#if headerSearchStore.query}
            <button
                type="button"
                onclick={() => {
                    headerSearchStore.clear();
                    inputEl?.focus();
                }}
                title="Clear search"
                aria-label="Clear search"
                class="shrink-0 cursor-pointer rounded-md p-0.5 text-on-surface-faint transition-colors hover:bg-surface-sunken hover:text-on-surface"
            >
                <X size={14} />
            </button>
        {:else}
            <kbd
                class="pointer-events-none hidden shrink-0 items-center rounded-md border border-outline-faint bg-surface-raised px-1.5 py-0.5 font-sans text-2xs font-medium text-on-surface-faint shadow-sm sm:flex"
            >
                {shortcut}
            </kbd>
        {/if}
    </div>
{/if}
