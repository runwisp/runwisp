<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";
    import type { HTMLAttributes } from "svelte/elements";

    type HeadingLevel = 1 | 2 | 3 | 4 | 5 | 6;
    type HeadingSize = "xs" | "sm" | "md" | "lg" | "xl" | "2xl" | "3xl" | "4xl";

    interface Props extends Omit<HTMLAttributes<HTMLHeadingElement>, "children" | "class"> {
        level: HeadingLevel;
        size?: HeadingSize;
        children: Snippet;
        class?: string;
    }

    let { level, size, children, class: className = "", ...restProps }: Props = $props();

    const defaultSizeMap: Record<HeadingLevel, HeadingSize> = {
        1: "4xl",
        2: "3xl",
        3: "2xl",
        4: "xl",
        5: "lg",
        6: "md",
    };

    const sizeClasses: Record<HeadingSize, string> = {
        xs: "text-xs",
        sm: "text-sm",
        md: "text-base",
        lg: "text-lg",
        xl: "text-xl",
        "2xl": "text-2xl",
        "3xl": "text-3xl",
        "4xl": "text-4xl",
    };

    const resolvedSize = $derived(size ?? defaultSizeMap[level]);
    const tag = $derived(`h${level}` as const);
</script>

<!-- theme.css styles raw h1-h3 globally; data-rw-heading opts out of that rule
     so these classes (the same runbook look: mono, 800, tight) decide. -->
<svelte:element
    this={tag}
    data-rw-heading
    class="font-mono leading-[1.1] font-extrabold tracking-[-0.02em] text-on-surface {sizeClasses[
        resolvedSize
    ]} {className}"
    {...restProps}
>
    {@render children()}
</svelte:element>
