<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";
    import type { HTMLButtonAttributes } from "svelte/elements";
    import Spinner from "./Spinner.svelte";
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
        icon?: Snippet | undefined;
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
        <Spinner size="sm" label="" />
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
