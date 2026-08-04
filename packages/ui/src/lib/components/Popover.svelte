<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";
    import type { Placement } from "@floating-ui/dom";
    import { onDestroy } from "svelte";
    import { computePosition, autoUpdate, flip, shift, offset } from "@floating-ui/dom";
    import { fade } from "svelte/transition";
    import { portal } from "../actions/portal.js";

    interface Props {
        open?: boolean;
        placement?: Placement;
        // Opt-in: below the `md` breakpoint, render the content as a bottom
        // sheet (full-width, pinned to the viewport bottom, dimmed scrim behind)
        // instead of a floating popover. Leaves desktop behavior untouched.
        mobileSheet?: boolean;
        trigger?: Snippet;
        children?: Snippet;
        class?: string;
    }

    let {
        open = $bindable(false),
        placement = "bottom",
        mobileSheet = false,
        trigger,
        children,
        class: className = "",
    }: Props = $props();

    let triggerEl = $state<HTMLElement | null>(null);
    let contentEl = $state<HTMLElement | null>(null);
    let cleanupFloating = $state<(() => void) | null>(null);

    // Track the `md` breakpoint only when sheet mode is requested, so the
    // content can switch between floating (desktop) and bottom sheet (phone)
    // live on resize. matchMedia is client-only; the effect never runs on SSR.
    let isDesktop = $state(true);
    $effect(() => {
        if (!mobileSheet) return;
        const mq = window.matchMedia("(min-width: 768px)");
        isDesktop = mq.matches;
        const onChange = (e: MediaQueryListEvent) => (isDesktop = e.matches);
        mq.addEventListener("change", onChange);
        return () => mq.removeEventListener("change", onChange);
    });

    const asSheet = $derived(mobileSheet && !isDesktop);

    function teardown() {
        cleanupFloating?.();
        cleanupFloating = null;
    }

    function setupFloating() {
        if (!triggerEl || !contentEl) return;
        cleanupFloating = autoUpdate(triggerEl, contentEl, () => {
            if (!triggerEl || !contentEl) return;
            void computePosition(triggerEl, contentEl, {
                placement,
                middleware: [offset(8), flip(), shift({ padding: 8 })],
            }).then(({ x, y }) => {
                if (contentEl) {
                    Object.assign(contentEl.style, {
                        left: `${String(x)}px`,
                        top: `${String(y)}px`,
                        position: "absolute",
                    });
                }
            });
        });
    }

    // Position the content while open: float it on desktop, or pin it as a
    // sheet on phone (clearing any inline coords floating-ui left behind).
    $effect(() => {
        if (!open || !contentEl) {
            teardown();
            return;
        }
        if (asSheet) {
            teardown();
            contentEl.style.removeProperty("left");
            contentEl.style.removeProperty("top");
            contentEl.style.removeProperty("position");
            return;
        }
        setupFloating();
        return teardown;
    });

    function toggle() {
        open = !open;
    }

    function handleOutsideClick(e: MouseEvent) {
        const path = e.composedPath();
        if (triggerEl && path.includes(triggerEl)) return;
        if (contentEl && path.includes(contentEl)) return;
        if (open) open = false;
    }

    function handleKeyDown(e: KeyboardEvent) {
        if (e.key === "Escape" && open) open = false;
    }

    onDestroy(teardown);
</script>

<svelte:window onclick={handleOutsideClick} onkeydown={handleKeyDown} />

<div class="inline-block {className}">
    <div
        bind:this={triggerEl}
        role="button"
        tabindex="0"
        onclick={(e) => {
            e.stopPropagation();
            toggle();
        }}
        onkeydown={(e) => {
            if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                e.stopPropagation();
                toggle();
            }
        }}
    >
        {#if trigger}
            {@render trigger()}
        {/if}
    </div>

    {#if open}
        {#if asSheet}
            <div
                use:portal
                transition:fade={{ duration: 120 }}
                class="fixed inset-0 z-[9998] bg-backdrop"
                aria-hidden="true"
            ></div>
        {/if}
        <div
            use:portal
            bind:this={contentEl}
            class="
                z-[9999] max-h-[80vh] overflow-y-auto
                border border-outline bg-surface-overlay shadow-lg
                {asSheet
                ? 'fixed inset-x-0 bottom-0 max-h-[85vh] rounded-t-[4px] border-x-0 border-b-0 p-5'
                : 'rounded-[4px] p-4'}
            "
        >
            {#if children}
                {@render children()}
            {/if}
        </div>
    {/if}
</div>
