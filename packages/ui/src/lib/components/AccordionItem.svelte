<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";
    import { ChevronDown } from "@lucide/svelte";

    interface Props {
        title?: Snippet | string;
        children?: Snippet;
        open?: boolean;
        disabled?: boolean;
        class?: string;
    }

    let {
        title,
        children,
        open = $bindable(false),
        disabled = false,
        class: className = "",
    }: Props = $props();

    function toggle() {
        if (!disabled) {
            open = !open;
        }
    }
</script>

<div class="group {className}">
    <button
        type="button"
        onclick={toggle}
        {disabled}
        class="
            flex w-full items-center justify-between gap-3 py-4 text-left font-mono text-sm
            text-on-surface hover:text-primary
            disabled:cursor-not-allowed disabled:opacity-50
        "
        aria-expanded={open}
    >
        <span class="flex-1">
            {#if typeof title === "function"}
                {@render title()}
            {:else if title}
                {title}
            {/if}
        </span>
        <ChevronDown size={16} class="shrink-0 text-on-surface-faint {open ? 'rotate-180' : ''}" />
    </button>

    <div
        class="grid transition-[grid-template-rows] duration-150 ease-out {open
            ? 'grid-rows-[1fr]'
            : 'grid-rows-[0fr]'}"
    >
        <div class="overflow-hidden">
            {#if children}
                <div class="pb-4 text-sm text-on-surface-muted">
                    {@render children()}
                </div>
            {/if}
        </div>
    </div>
</div>
