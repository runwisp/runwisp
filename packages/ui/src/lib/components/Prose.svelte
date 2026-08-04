<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";

    type ProseSize = "sm" | "base" | "lg";

    interface Props {
        size?: ProseSize;
        children: Snippet;
        class?: string;
    }

    let { size = "base", children, class: className = "" }: Props = $props();

    const sizeClasses: Record<ProseSize, string> = {
        sm: "prose-sm",
        base: "prose-base",
        lg: "prose-lg",
    };
</script>

<div
    class="
        prose {sizeClasses[size]}
        max-w-none
        prose-headings:text-on-surface
        prose-p:text-on-surface-muted prose-a:text-primary prose-a:no-underline
        hover:prose-a:underline
        prose-blockquote:border-outline prose-blockquote:text-on-surface-muted prose-strong:text-on-surface prose-code:rounded-[3px] prose-code:bg-surface-sunken
        prose-code:px-1 prose-code:py-0.5 prose-code:font-mono prose-code:text-on-surface
        prose-pre:rounded-[4px] prose-pre:border prose-pre:border-outline
        prose-pre:bg-surface-sunken prose-pre:font-mono
        prose-li:text-on-surface-muted
        prose-th:text-on-surface
        prose-td:text-on-surface-muted
        prose-hr:border-outline-faint
        {className}
    "
>
    {@render children()}
</div>
