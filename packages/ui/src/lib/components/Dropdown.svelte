<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";
    import type { Component } from "svelte";
    import { tick, onDestroy } from "svelte";
    import { EllipsisVertical } from "@lucide/svelte";
    import { scale } from "svelte/transition";
    import { quintOut } from "svelte/easing";
    import { computePosition, autoUpdate, flip, shift, offset } from "@floating-ui/dom";

    interface MenuItem {
        label?: string;
        icon?: Component;
        href?: string;
        onClick?: () => void;
        danger?: boolean;
        disabled?: boolean;
        divider?: boolean;
        title?: string;
    }

    interface Props {
        items?: MenuItem[];
        trigger?: Snippet;
        children?: Snippet;
        align?: "left" | "right";
        class?: string;
    }

    let { items = [], trigger, children, align = "right", class: className = "" }: Props = $props();

    let open = $state(false);
    let triggerEl = $state<HTMLElement | null>(null);
    let menuEl = $state<HTMLElement | null>(null);
    let cleanupFloating = $state<(() => void) | null>(null);
    let transformOrigin = $state("top right");

    import { portal } from "../actions/portal.js";

    async function setupFloating() {
        if (!triggerEl || !menuEl) return;

        cleanupFloating = autoUpdate(triggerEl, menuEl, () => {
            if (!triggerEl || !menuEl) return;

            const placement = align === "right" ? "bottom-end" : "bottom-start";

            void computePosition(triggerEl, menuEl, {
                placement,
                middleware: [
                    offset(4), // MENU_OFFSET
                    flip(),
                    shift({ padding: 8 }), // VIEWPORT_PADDING
                ],
            }).then(({ x, y, placement }) => {
                if (menuEl) {
                    Object.assign(menuEl.style, {
                        left: `${x}px`,
                        top: `${y}px`,
                        position: "absolute",
                    });

                    // Simple origin logic based on verify placement
                    const isTop = placement.startsWith("top");
                    transformOrigin = `${isTop ? "bottom" : "top"} ${align}`;
                }
            });
        });
    }

    function handleItemClick(item: MenuItem) {
        if (item.divider) return;
        if (item.disabled) return;
        item.onClick?.();
        toggle(false);
    }

    function toggle(value: boolean) {
        open = value;
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
        const path = e.composedPath();
        if (triggerEl && path.includes(triggerEl)) return;
        if (menuEl && path.includes(menuEl)) return;
        if (open) toggle(false);
    }

    function handleKeyDown(e: KeyboardEvent) {
        if (e.key === "Escape" && open) {
            toggle(false);
        }
    }

    onDestroy(() => {
        cleanupFloating?.();
    });
</script>

<svelte:window onclick={handleOutsideClick} onkeydown={handleKeyDown} />

<div class="dropdown-container relative inline-block {className}">
    <button
        onclick={(e) => {
            e.stopPropagation();
            toggle(!open);
        }}
        bind:this={triggerEl}
        aria-label="Options"
        class="rounded-lg p-2 text-on-surface-muted transition-colors hover:bg-surface-sunken hover:text-on-surface focus:ring-2 focus:ring-outline focus:outline-none active:bg-surface-sunken"
    >
        {#if trigger}
            {@render trigger()}
        {:else}
            <EllipsisVertical size={18} />
        {/if}
    </button>

    {#if open}
        <div
            use:portal
            bind:this={menuEl}
            role="menu"
            transition:scale={{ duration: 150, start: 0.95, easing: quintOut }}
            class="
				z-[9999]
				min-w-48 overflow-y-auto
				rounded-xl
				border border-outline bg-surface-overlay py-1 shadow-xl shadow-on-surface/5
			"
            style="transform-origin: {transformOrigin};"
        >
            {#if children}
                {@render children()}
            {:else}
                {#each items as item, idx (idx)}
                    {#if item.divider}
                        <hr class="my-1 border-outline-faint" />
                    {:else if item.href}
                        <a
                            href={item.href}
                            title={item.title}
                            role="menuitem"
                            class="
									mx-1 flex items-center gap-3 rounded-lg px-3 py-2 text-sm
									{item.danger
                                ? 'text-danger-soft-text hover:bg-danger-soft'
                                : 'text-on-surface-muted hover:bg-surface-sunken'}
									{item.disabled ? 'cursor-not-allowed opacity-50' : ''}
								transition-colors
							"
                        >
                            {#if item.icon}
                                {@const Icon = item.icon}
                                <Icon size={16} class="opacity-70" />
                            {/if}
                            {item.label}
                        </a>
                    {:else}
                        <button
                            onclick={() => handleItemClick(item)}
                            disabled={item.disabled}
                            title={item.title}
                            role="menuitem"
                            class="
									mx-1 box-border flex w-full max-w-[calc(100%-8px)] items-center gap-3 rounded-lg px-3 py-2 text-left text-sm
									{item.danger
                                ? 'text-danger-soft-text hover:bg-danger-soft'
                                : 'text-on-surface-muted hover:bg-surface-sunken'}
									transition-colors disabled:cursor-not-allowed
									disabled:opacity-50
							"
                        >
                            {#if item.icon}
                                {@const Icon = item.icon}
                                <Icon size={16} class="opacity-70" />
                            {/if}
                            {item.label}
                        </button>
                    {/if}
                {/each}
            {/if}
        </div>
    {/if}
</div>
