<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";

    type CellAlign = "left" | "center" | "right";

    interface Props {
        header?: boolean;
        align?: CellAlign;
        children: Snippet;
        class?: string;
    }

    let { header = false, align = "left", children, class: className = "" }: Props = $props();

    const alignClasses: Record<CellAlign, string> = {
        left: "text-left",
        center: "text-center",
        right: "text-right",
    };

    const tag = $derived(header ? "th" : "td");
</script>

<svelte:element
    this={tag}
    class="px-4 py-3 {alignClasses[align]} {header
        ? 'font-mono text-xs tracking-wide text-on-surface-faint uppercase'
        : 'text-on-surface'} {className}"
>
    {@render children()}
</svelte:element>
