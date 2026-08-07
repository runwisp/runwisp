<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";
    import type { Component } from "svelte";
    import { tick, onDestroy } from "svelte";
    import { EllipsisVertical, Check } from "@lucide/svelte";
    import { computePosition, autoUpdate, flip, shift, offset } from "@floating-ui/dom";

    interface MenuItem {
        label?: string;
        icon?: Component;
        href?: string;
        onClick?: () => void;
        danger?: boolean;
        disabled?: boolean;
        divider?: boolean;
        selected?: boolean;
        title?: string;
    }

    interface Props {
        items?: MenuItem[];
        trigger?: Snippet;
        children?: Snippet;
        align?: "left" | "right";
        triggerLabel?: string;
        class?: string;
    }

    let {
        items = [],
        trigger,
        children,
        align = "right",
        triggerLabel = "Options",
        class: className = "",
    }: Props = $props();

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
                middleware: [offset(4), flip(), shift({ padding: 8 })],
            }).then(({ x, y, placement }) => {
                if (menuEl) {
                    Object.assign(menuEl.style, {
                        left: `${x}px`,
                        top: `${y}px`,
                        position: "absolute",
                    });

                    // Flip transform origin vertically when the menu flips above the trigger
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
        aria-label={triggerLabel}
        aria-expanded={open}
        class="rounded-[3px] p-2 text-on-surface-muted hover:bg-surface-sunken hover:text-on-surface focus:ring-2 focus:ring-outline focus:outline-none active:bg-surface-sunken"
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
            class="
				z-[9999]
				min-w-48 overflow-y-auto
				rounded-[4px]
				border border-outline bg-surface-overlay py-1 shadow-lg
			"
            style="transform-origin: {transformOrigin};"
        >
            {#if children}
                {@render children()}
            {:else}
                {#each items as item, idx (idx)}
                    {#if item.divider}
                        <hr class="my-1 border-outline" />
                    {:else if item.href}
                        <a
                            href={item.href}
                            title={item.title}
                            role="menuitem"
                            class="
									mx-1 flex items-center gap-3 rounded-[3px] px-3 py-2 font-mono text-sm
									{item.danger
                                ? 'text-danger-soft-text hover:bg-danger-soft'
                                : 'text-on-surface-muted hover:bg-surface-sunken hover:text-on-surface'}
									{item.disabled ? 'cursor-not-allowed opacity-50' : ''}
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
									mx-1 box-border flex w-full max-w-[calc(100%-8px)] items-center gap-3 rounded-[3px] px-3 py-2 text-left font-mono text-sm
									{item.danger
                                ? 'text-danger-soft-text hover:bg-danger-soft'
                                : 'text-on-surface-muted hover:bg-surface-sunken hover:text-on-surface'}
									disabled:cursor-not-allowed
									disabled:opacity-50
							"
                        >
                            {#if item.icon}
                                {@const Icon = item.icon}
                                <Icon size={16} class="opacity-70" />
                            {/if}
                            {item.label}
                            {#if item.selected}
                                <Check size={16} class="ml-auto text-primary" />
                            {/if}
                        </button>
                    {/if}
                {/each}
            {/if}
        </div>
    {/if}
</div>
