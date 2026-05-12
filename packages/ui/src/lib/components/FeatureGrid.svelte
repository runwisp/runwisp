<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";

    interface Props {
        columns?: 1 | 2 | 3 | 4;
        gap?: "sm" | "md" | "lg";
        children?: Snippet;
        class?: string;
    }

    let { columns = 3, gap = "md", children, class: className = "" }: Props = $props();

    const colClasses: Record<1 | 2 | 3 | 4, string> = {
        1: "grid-cols-1",
        2: "grid-cols-1 sm:grid-cols-2",
        3: "grid-cols-1 sm:grid-cols-2 lg:grid-cols-3",
        4: "grid-cols-1 sm:grid-cols-2 lg:grid-cols-4",
    };

    const gapClasses: Record<"sm" | "md" | "lg", string> = {
        sm: "gap-3",
        md: "gap-4",
        lg: "gap-6",
    };

    const colClass = $derived(colClasses[columns]);
    const gapClass = $derived(gapClasses[gap]);
</script>

<div class="grid {colClass} {gapClass} {className}">
    {#if children}
        {@render children()}
    {/if}
</div>
