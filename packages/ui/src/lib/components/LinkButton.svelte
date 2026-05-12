<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";
    import type { HTMLAnchorAttributes } from "svelte/elements";
    import {
        BUTTON_BASE,
        BUTTON_SIZES,
        BUTTON_VARIANTS,
        type ButtonSize,
        type ButtonVariant,
    } from "./button-styles.js";

    interface Props extends HTMLAnchorAttributes {
        variant?: ButtonVariant;
        size?: ButtonSize;
        fullWidth?: boolean;
        icon?: Snippet;
        iconRight?: Snippet;
        children?: Snippet;
    }

    let {
        variant = "primary",
        size = "md",
        fullWidth = false,
        icon,
        iconRight,
        children,
        class: className = "",
        ...restProps
    }: Props = $props();
</script>

<a
    class="{BUTTON_BASE} {BUTTON_VARIANTS[variant]} {BUTTON_SIZES[size]} {fullWidth
        ? 'w-full'
        : ''} {className}"
    {...restProps}
>
    {#if icon}
        <span class="shrink-0 transition-transform group-active:scale-95">
            {@render icon()}
        </span>
    {/if}
    {#if children}
        {@render children()}
    {/if}
    {#if iconRight}
        <span class="shrink-0 transition-transform group-active:scale-95">
            {@render iconRight()}
        </span>
    {/if}
</a>
