<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { getContext, untrack, type Snippet } from "svelte";
    import { ChevronDown } from "@lucide/svelte";
    import { ACCORDION_KEY, type AccordionGroup } from "./accordion.svelte.js";

    /** `framed` is a standalone bordered panel; `plain` is a row inside an Accordion. */
    type AccordionItemVariant = "plain" | "framed";

    interface Props {
        title?: Snippet | string;
        /** Controls beside the header that must not toggle it (a switch, a delete button). */
        actions?: Snippet;
        children?: Snippet;
        open?: boolean;
        disabled?: boolean;
        variant?: AccordionItemVariant;
        class?: string;
    }

    let {
        title,
        actions,
        children,
        open = $bindable(false),
        disabled = false,
        variant = "plain",
        class: className = "",
    }: Props = $props();

    const id = $props.id();
    const group = getContext<AccordionGroup | undefined>(ACCORDION_KEY);

    // Single mode: claim the group when this item opens (by click or binding)…
    $effect(() => {
        if (open && group?.single) {
            untrack(() => {
                group.active = id;
            });
        }
    });

    // …and close when another item claims it.
    $effect(() => {
        const active = group?.active;
        if (active && active !== id) {
            untrack(() => {
                if (open) open = false;
            });
        }
    });

    function toggle() {
        if (!disabled) {
            open = !open;
        }
    }

    const framed = $derived(variant === "framed");
</script>

<div
    class="group {framed
        ? 'rounded-[4px] border border-outline bg-surface-raised'
        : ''} {className}"
>
    <div class="flex items-center gap-3 {framed ? 'px-4' : ''}">
        <button
            type="button"
            onclick={toggle}
            {disabled}
            class="
                flex min-w-0 flex-1 items-center justify-between gap-3 text-left font-mono text-sm
                text-on-surface hover:text-primary
                disabled:cursor-not-allowed disabled:opacity-50
                {framed ? 'py-3' : 'py-4'}
            "
            aria-expanded={open}
            aria-controls="{id}-content"
        >
            <span class="min-w-0 flex-1">
                {#if typeof title === "function"}
                    {@render title()}
                {:else if title}
                    {title}
                {/if}
            </span>
            <ChevronDown
                size={16}
                class="shrink-0 text-on-surface-faint {open ? 'rotate-180' : ''}"
            />
        </button>
        {#if actions}
            <div class="flex shrink-0 items-center gap-2">
                {@render actions()}
            </div>
        {/if}
    </div>

    <!-- Collapsed content stays mounted for the height transition, so `inert`
         keeps it out of the tab order and the accessibility tree. -->
    <div
        id="{id}-content"
        inert={!open}
        class="grid transition-[grid-template-rows] duration-150 ease-out {open
            ? 'grid-rows-[1fr]'
            : 'grid-rows-[0fr]'}"
    >
        <div class="overflow-hidden">
            {#if children}
                <div
                    class="text-sm text-on-surface-muted {framed
                        ? 'border-t border-outline-faint p-4'
                        : 'pb-4'}"
                >
                    {@render children()}
                </div>
            {/if}
        </div>
    </div>
</div>
