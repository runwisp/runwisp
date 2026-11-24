<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { CaseSensitive, ChevronDown, ChevronUp, Regex, Search, X } from "@lucide/svelte";
    import Kbd from "../components/Kbd.svelte";
    import type { LogFind } from "./log-find.svelte.js";

    let {
        find,
        input = $bindable(null),
        onClose,
    }: {
        find: LogFind;
        input?: HTMLInputElement | null;
        onClose: () => void;
    } = $props();

    const count = $derived(find.matches.length);
    const noMatches = $derived(find.status === "done" && count === 0 && find.query !== "");
    const label = $derived.by(() => {
        if (!find.query) return "";
        if (find.status === "error") return find.error ?? "Search failed";
        if (find.status === "searching" && count === 0) return "Searching…";
        if (noMatches) return "No matches";
        const position = find.index >= 0 ? (find.index + 1).toLocaleString() : "–";
        return `${position} of ${count.toLocaleString()}${find.capped ? "+" : ""}`;
    });

    // What a key does in the find box. Shift+Home/End are left to the input
    // so they still select text.
    function keyAction(e: KeyboardEvent): (() => void) | undefined {
        const plain: Record<string, () => void> = {
            Enter: () => (e.shiftKey ? find.prev() : find.next()),
            PageUp: () => find.page(-1),
            PageDown: () => find.page(1),
            ...(e.shiftKey ? {} : { Home: () => find.first(), End: () => find.last() }),
        };
        if (!e.altKey) return plain[e.key];
        if (e.code === "KeyC") return () => (find.caseSensitive = !find.caseSensitive);
        if (e.code === "KeyR") return () => (find.regex = !find.regex);
        return undefined;
    }

    function onKeydown(e: KeyboardEvent) {
        if (e.key === "Escape") {
            // Stop here so a maximized console stays maximized.
            e.stopPropagation();
            onClose();
            return;
        }
        const action = keyAction(e);
        if (!action) return;
        e.preventDefault();
        action();
    }

    function keepFocus(e: MouseEvent) {
        if (e.target instanceof Element && e.target.closest("button")) e.preventDefault();
    }

    const invalid = $derived(noMatches || find.status === "error");

    // Toggles sit inside the field, VS Code style: a quiet glyph that fills
    // with the accent when on.
    const toggleClass = (on: boolean) =>
        `flex size-6 shrink-0 cursor-pointer items-center justify-center rounded-[3px] transition-colors ${
            on
                ? "bg-term-teal/15 text-term-teal ring-1 ring-term-teal/40"
                : "text-[var(--rw-con-dim)] hover:bg-[var(--rw-con-gutter)]/30 hover:text-[var(--rw-con-text)]"
        }`;
    const iconButton =
        "flex size-6 shrink-0 cursor-pointer items-center justify-center rounded-[3px] text-[var(--rw-con-dim)] transition-colors hover:bg-[var(--rw-con-gutter)]/30 hover:text-[var(--rw-con-text)] disabled:cursor-default disabled:opacity-35 disabled:hover:bg-transparent";
</script>

<!-- Mouse clicks on the bar's buttons leave focus in the input, so typing
     carries on; keyboard users still tab to them. -->
<div
    onmousedown={keepFocus}
    class="find-bar @container flex shrink-0 items-center gap-2 border-b border-[var(--rw-con-gutter)] bg-[var(--rw-con-panel)] px-3.5 py-1.5 font-mono text-[11.5px] text-[var(--rw-con-dim)]"
    role="search"
>
    <!-- The field draws its own focus ring, so the input's outline is off. -->
    <div
        class="flex h-7 min-w-0 flex-1 items-center gap-1.5 rounded-[3px] bg-[var(--rw-con-bg)] pr-0.5 pl-2 ring-1 transition-shadow @md:max-w-[26rem] {invalid
            ? 'ring-term-bad/60 focus-within:ring-term-bad'
            : 'ring-[var(--rw-con-gutter)]/70 focus-within:ring-term-teal/70'}"
    >
        <Search size={13} class="shrink-0 opacity-60" />
        <input
            bind:this={input}
            bind:value={find.query}
            onkeydown={onKeydown}
            type="text"
            spellcheck="false"
            autocomplete="off"
            placeholder="Find in this log"
            aria-label="Find in this log"
            aria-invalid={invalid}
            class="h-full min-w-0 flex-1 border-0 bg-transparent p-0 font-mono text-[12px] outline-none! placeholder:text-[var(--rw-con-gutter)] focus:[box-shadow:none] {invalid
                ? 'text-term-bad'
                : 'text-[var(--rw-con-text)]'}"
        />
        <button
            type="button"
            class={toggleClass(find.caseSensitive)}
            aria-pressed={find.caseSensitive}
            aria-label="Match case"
            title="Match case (Alt+C)"
            onclick={() => (find.caseSensitive = !find.caseSensitive)}
        >
            <CaseSensitive size={15} />
        </button>
        <button
            type="button"
            class={toggleClass(find.regex)}
            aria-pressed={find.regex}
            aria-label="Regular expression"
            title="Regular expression (Alt+R)"
            onclick={() => (find.regex = !find.regex)}
        >
            <Regex size={14} />
        </button>
    </div>
    <span
        class="min-w-[9ch] truncate whitespace-nowrap tabular-nums {invalid ? 'text-term-bad' : ''}"
        aria-live="polite"
        data-testid="log-find-count"
    >
        {label}
    </span>
    <div class="flex shrink-0 items-center">
        <button
            type="button"
            class={iconButton}
            disabled={count === 0}
            aria-label="Previous match"
            title="Previous match (Shift+Enter)"
            onclick={() => find.prev()}
        >
            <ChevronUp size={15} />
        </button>
        <button
            type="button"
            class={iconButton}
            disabled={count === 0}
            aria-label="Next match"
            title="Next match (Enter)"
            onclick={() => find.next()}
        >
            <ChevronDown size={15} />
        </button>
    </div>
    <span class="ml-auto hidden items-center gap-1.5 whitespace-nowrap @2xl:flex">
        <Kbd keys="Enter" size="xs" tone="console" />
        <span>next</span>
        <Kbd keys="Shift+Enter" size="xs" tone="console" class="ml-1" />
        <span>prev</span>
    </span>
    <button
        type="button"
        class="{iconButton} ml-auto @2xl:ml-1"
        aria-label="Close find"
        title="Close (Esc)"
        onclick={onClose}
    >
        <X size={15} />
    </button>
</div>

<style>
    /* Drop in from the toolbar; skipped under reduced motion. */
    .find-bar {
        animation: find-bar-in 120ms ease-out;
    }
    @keyframes find-bar-in {
        from {
            opacity: 0;
            transform: translateY(-3px);
        }
    }
    @media (prefers-reduced-motion: reduce) {
        .find-bar {
            animation: none;
        }
    }
</style>
