<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";
    import type { HTMLButtonAttributes } from "svelte/elements";
    import {
        BUTTON_BASE,
        BUTTON_SIZES,
        BUTTON_VARIANTS,
        type ButtonSize,
        type ButtonVariant,
    } from "./button-styles.js";

    interface Props extends HTMLButtonAttributes {
        variant?: ButtonVariant;
        size?: ButtonSize;
        fullWidth?: boolean;
        loading?: boolean;
        icon?: Snippet;
        iconRight?: Snippet;
        children?: Snippet;
    }

    let {
        variant = "primary",
        size = "md",
        fullWidth = false,
        loading = false,
        disabled = false,
        icon,
        iconRight,
        children,
        class: className = "",
        ...restProps
    }: Props = $props();
</script>

<button
    class="{BUTTON_BASE} {BUTTON_VARIANTS[variant]} {BUTTON_SIZES[size]} {fullWidth
        ? 'w-full'
        : ''} {className}"
    disabled={disabled || loading}
    {...restProps}
>
    {#if loading}
        <svg class="h-4 w-4 animate-spin" viewBox="0 0 24 24" fill="none">
            <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"
            ></circle>
            <path
                class="opacity-75"
                fill="currentColor"
                d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
            ></path>
        </svg>
    {:else if icon}
        <span class="shrink-0 group-active:scale-95">
            {@render icon()}
        </span>
    {/if}
    {#if children}
        {@render children()}
    {/if}
    {#if iconRight && !loading}
        <span class="shrink-0 group-active:scale-95">
            {@render iconRight()}
        </span>
    {/if}
</button>
