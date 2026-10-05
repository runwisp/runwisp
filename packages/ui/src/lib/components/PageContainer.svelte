<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";

    /** The standard page-level wrapper. See variantClasses for each variant's layout. */
    let {
        children,
        variant = "standard",
        class: className = "",
    }: {
        children: Snippet;
        variant?: "standard" | "full" | "wide" | "narrow" | "flush";
        class?: string;
    } = $props();

    const variantClasses: Record<string, string> = {
        standard: "mx-auto w-full max-w-7xl flex flex-col min-h-full p-4 md:p-8",
        full: "mx-auto w-full max-w-7xl flex flex-col h-full min-h-0 p-4 md:p-8",
        wide: "mx-auto w-full max-w-7xl",
        narrow: "mx-auto w-full max-w-3xl",
        flush: "flex flex-col h-full min-h-0",
    };
</script>

<div class="{variantClasses[variant]} {className}">
    {@render children()}
</div>
