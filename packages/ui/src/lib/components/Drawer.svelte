<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";
    import { X } from "@lucide/svelte";
    import { portal } from "../actions/portal.js";
    import { trapFocus } from "../actions/focusTrap.js";
    import { fly } from "svelte/transition";

    type Side = "left" | "right" | "top" | "bottom";
    type DrawerSize = "sm" | "md" | "lg" | "xl";

    interface Props {
        open?: boolean;
        side?: Side;
        size?: DrawerSize;
        title?: string;
        closable?: boolean;
        onClose?: () => void;
        header?: Snippet;
        footer?: Snippet;
        children?: Snippet;
        class?: string;
    }

    let {
        open = $bindable(false),
        side = "right",
        size = "md",
        title,
        closable = true,
        onClose,
        header,
        footer,
        children,
        class: className = "",
    }: Props = $props();

    const sizeValues: Record<DrawerSize, string> = {
        sm: "320px",
        md: "400px",
        lg: "560px",
        xl: "720px",
    };

    const isHorizontal = $derived(side === "left" || side === "right");

    const panelStyle = $derived(
        isHorizontal
            ? `width: ${sizeValues[size]}; max-width: 100vw;`
            : `height: ${sizeValues[size]}; max-height: 100vh;`,
    );

    const positionClasses: Record<Side, string> = {
        left: "inset-y-0 left-0",
        right: "inset-y-0 right-0",
        top: "inset-x-0 top-0",
        bottom: "inset-x-0 bottom-0",
    };

    const flyParams: Record<Side, { x?: number; y?: number }> = {
        left: { x: -320 },
        right: { x: 320 },
        top: { y: -320 },
        bottom: { y: 320 },
    };

    function handleClose() {
        open = false;
        onClose?.();
    }

    function handleKeydown(e: KeyboardEvent) {
        if (e.key === "Escape" && closable) {
            handleClose();
        }
    }
</script>

<svelte:window onkeydown={handleKeydown} />

{#if open}
    <div use:portal class="fixed inset-0 z-50">
        {#if closable}
            <button
                type="button"
                class="absolute inset-0 z-0 bg-backdrop backdrop-blur-sm"
                aria-label="Close drawer"
                tabindex="-1"
                onclick={handleClose}
                transition:fly={{ duration: 150 }}
            ></button>
        {:else}
            <div
                class="absolute inset-0 z-0 bg-backdrop backdrop-blur-sm"
                transition:fly={{ duration: 150 }}
            ></div>
        {/if}

        <div
            use:trapFocus
            class="
                absolute z-10 flex flex-col
                border border-outline bg-surface-overlay shadow-lg
                {positionClasses[side]}
                {className}
            "
            style={panelStyle}
            role="dialog"
            aria-modal="true"
            aria-labelledby={title ? "drawer-title" : undefined}
            transition:fly={{ ...flyParams[side], duration: 250 }}
        >
            {#if header}
                <div class="border-b border-outline px-6 py-4">
                    {@render header()}
                </div>
            {:else if title || closable}
                <div
                    class="flex items-start justify-between gap-4 border-b border-outline px-6 py-4"
                >
                    <div>
                        {#if title}
                            <h2
                                id="drawer-title"
                                class="font-mono text-lg font-semibold text-on-surface"
                            >
                                {title}
                            </h2>
                        {/if}
                    </div>
                    {#if closable}
                        <button
                            onclick={handleClose}
                            class="-m-2 shrink-0 rounded-[3px] p-2 text-on-surface-faint hover:bg-surface-sunken hover:text-on-surface-muted"
                            aria-label="Close"
                        >
                            <X size={20} />
                        </button>
                    {/if}
                </div>
            {/if}

            {#if children}
                <div class="flex-1 overflow-y-auto px-6 py-4">
                    {@render children()}
                </div>
            {/if}

            {#if footer}
                <div class="border-t border-outline bg-surface-sunken px-6 py-4">
                    {@render footer()}
                </div>
            {/if}
        </div>
    </div>
{/if}
