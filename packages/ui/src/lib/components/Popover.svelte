<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";
    import type { Placement } from "@floating-ui/dom";
    import { tick, onDestroy } from "svelte";
    import { computePosition, autoUpdate, flip, shift, offset } from "@floating-ui/dom";
    import { scale } from "svelte/transition";
    import { quintOut } from "svelte/easing";
    import { portal } from "../actions/portal.js";

    interface Props {
        open?: boolean;
        placement?: Placement;
        trigger?: Snippet;
        children?: Snippet;
        class?: string;
    }

    let {
        open = $bindable(false),
        placement = "bottom",
        trigger,
        children,
        class: className = "",
    }: Props = $props();

    let triggerEl = $state<HTMLElement | null>(null);
    let contentEl = $state<HTMLElement | null>(null);
    let cleanupFloating = $state<(() => void) | null>(null);

    async function setupFloating() {
        if (!triggerEl || !contentEl) return;

        cleanupFloating = autoUpdate(triggerEl, contentEl, () => {
            if (!triggerEl || !contentEl) return;

            void computePosition(triggerEl, contentEl, {
                placement,
                middleware: [offset(8), flip(), shift({ padding: 8 })],
            }).then(({ x, y }) => {
                if (contentEl) {
                    Object.assign(contentEl.style, {
                        left: `${x}px`,
                        top: `${y}px`,
                        position: "absolute",
                    });
                }
            });
        });
    }

    function toggle() {
        open = !open;
        if (open) {
            void tick().then(() => {
                void setupFloating();
            });
        } else {
            cleanupFloating?.();
            cleanupFloating = null;
        }
    }

    function handleOutsideClick(e: MouseEvent) {
        const target = e.target as HTMLElement;
        if (triggerEl?.contains(target)) return;
        if (contentEl?.contains(target)) return;
        if (open) {
            open = false;
            cleanupFloating?.();
            cleanupFloating = null;
        }
    }

    function handleKeyDown(e: KeyboardEvent) {
        if (e.key === "Escape" && open) {
            open = false;
            cleanupFloating?.();
            cleanupFloating = null;
        }
    }

    onDestroy(() => {
        cleanupFloating?.();
    });
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
        <div
            use:portal
            bind:this={contentEl}
            transition:scale={{ duration: 150, start: 0.95, easing: quintOut }}
            class="
                z-[9999]
                rounded-xl
                border border-outline bg-surface-overlay p-4 shadow-xl shadow-on-surface/5
            "
        >
            {#if children}
                {@render children()}
            {/if}
        </div>
    {/if}
</div>
