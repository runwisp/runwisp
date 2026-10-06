<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";
    import type { HTMLTdAttributes, HTMLThAttributes } from "svelte/elements";

    type CellAlign = "left" | "center" | "right";
    type CellDensity = "compact" | "default" | "comfortable" | "none";

    interface Props extends Omit<
        HTMLTdAttributes & HTMLThAttributes,
        "align" | "children" | "class"
    > {
        header?: boolean;
        align?: CellAlign | undefined;
        density?: CellDensity;
        children?: Snippet;
        class?: string;
    }

    let {
        header = false,
        align = "left",
        density = "default",
        children,
        class: className = "",
        ...restProps
    }: Props = $props();

    const alignClasses: Record<CellAlign, string> = {
        left: "text-left",
        center: "text-center",
        right: "text-right",
    };

    const densityClasses: Record<CellDensity, string> = {
        compact: "px-3 py-2",
        default: "px-4 py-3",
        comfortable: "px-5 py-3.5",
        none: "",
    };

    // A header with aria-sort set lights up with a primary underline, so a
    // sorted column is marked for sighted users and screen readers alike.
    const headerClasses = `
        font-mono text-xs tracking-[0.08em] text-on-surface-faint uppercase
        aria-[sort=ascending]:text-on-surface aria-[sort=descending]:text-on-surface
        aria-[sort=ascending]:shadow-[inset_0_-2px_0_var(--color-primary)]
        aria-[sort=descending]:shadow-[inset_0_-2px_0_var(--color-primary)]
    `;

    const tag = $derived(header ? "th" : "td");
</script>

<svelte:element
    this={tag}
    class="{densityClasses[density]} {alignClasses[align]} {header
        ? headerClasses
        : ''} {className}"
    {...restProps}
>
    {@render children?.()}
</svelte:element>
