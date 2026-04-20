<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";

    type TextSize = "xs" | "sm" | "base" | "lg" | "xl";
    type TextWeight = "normal" | "medium" | "semibold" | "bold";
    type TextColor = "default" | "muted" | "faint";
    type TextElement = "p" | "span" | "div";

    interface Props {
        size?: TextSize;
        weight?: TextWeight;
        color?: TextColor;
        as?: TextElement;
        children: Snippet;
        class?: string;
    }

    let {
        size = "base",
        weight = "normal",
        color = "default",
        as = "p",
        children,
        class: className = "",
    }: Props = $props();

    const sizeClasses: Record<TextSize, string> = {
        xs: "text-xs",
        sm: "text-sm",
        base: "text-base",
        lg: "text-lg",
        xl: "text-xl",
    };

    const weightClasses: Record<TextWeight, string> = {
        normal: "font-normal",
        medium: "font-medium",
        semibold: "font-semibold",
        bold: "font-bold",
    };

    const colorClasses: Record<TextColor, string> = {
        default: "text-on-surface",
        muted: "text-on-surface-muted",
        faint: "text-on-surface-faint",
    };
</script>

<svelte:element
    this={as}
    class="{sizeClasses[size]} {weightClasses[weight]} {colorClasses[color]} {className}"
>
    {@render children()}
</svelte:element>
