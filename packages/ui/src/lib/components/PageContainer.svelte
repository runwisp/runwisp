<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";

    /**
     * PageContainer — the single, standardized page-level wrapper.
     *
     * Variants:
     *   "standard"   – Max-width + centering + padding + flex-col + min-h-full.
     *   "full"       – Same as standard but h-full (fills viewport).
     *   "wide"       – Max-width + centering only (consumer provides padding).
     *   "narrow"     – Narrower max-width + centering.
     *   "flush"      – Fills container height, no max-width.
     */
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

<div class="animate-in fade-in duration-300 {variantClasses[variant]} {className}">
    {@render children()}
</div>
