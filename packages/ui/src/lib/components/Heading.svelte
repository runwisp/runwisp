<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";

    type HeadingLevel = 1 | 2 | 3 | 4 | 5 | 6;
    type HeadingSize = "xs" | "sm" | "md" | "lg" | "xl" | "2xl" | "3xl" | "4xl";

    interface Props {
        level: HeadingLevel;
        size?: HeadingSize;
        children: Snippet;
        class?: string;
    }

    let { level, size, children, class: className = "" }: Props = $props();

    const defaultSizeMap: Record<HeadingLevel, HeadingSize> = {
        1: "4xl",
        2: "3xl",
        3: "2xl",
        4: "xl",
        5: "lg",
        6: "base" as HeadingSize,
    };

    const sizeClasses: Record<string, string> = {
        xs: "text-xs font-semibold",
        sm: "text-sm font-semibold",
        base: "text-base font-semibold",
        md: "text-base font-semibold",
        lg: "text-lg font-semibold",
        xl: "text-xl font-bold",
        "2xl": "text-2xl font-bold",
        "3xl": "text-3xl font-bold",
        "4xl": "text-4xl font-bold tracking-tight",
    };

    const resolvedSize = $derived(size ?? defaultSizeMap[level]);
    const tag = $derived(`h${level}` as const);
</script>

<svelte:element this={tag} class="text-on-surface {sizeClasses[resolvedSize]} {className}">
    {@render children()}
</svelte:element>
