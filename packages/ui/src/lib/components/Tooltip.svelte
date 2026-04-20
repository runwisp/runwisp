<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";

    type TooltipPosition = "top" | "bottom" | "left" | "right";

    interface Props {
        content: string;
        position?: TooltipPosition;
        children: Snippet;
        class?: string;
    }

    let { content, position = "top", children, class: className = "" }: Props = $props();

    const positionClasses: Record<TooltipPosition, string> = {
        top: "bottom-full left-1/2 -translate-x-1/2 mb-2",
        bottom: "top-full left-1/2 -translate-x-1/2 mt-2",
        left: "right-full top-1/2 -translate-y-1/2 mr-2",
        right: "left-full top-1/2 -translate-y-1/2 ml-2",
    };

    const arrowClasses: Record<TooltipPosition, string> = {
        top: "top-full left-1/2 -translate-x-1/2 border-t-mist-800 border-x-transparent border-b-transparent",
        bottom: "bottom-full left-1/2 -translate-x-1/2 border-b-mist-800 border-x-transparent border-t-transparent",
        left: "left-full top-1/2 -translate-y-1/2 border-l-mist-800 border-y-transparent border-r-transparent",
        right: "right-full top-1/2 -translate-y-1/2 border-r-mist-800 border-y-transparent border-l-transparent",
    };
</script>

<div class="group relative inline-block {className}">
    {@render children()}

    <div
        class="
			absolute z-50 {positionClasses[position]}
			pointer-events-none invisible rounded-lg
			bg-mist-800 px-2.5 py-1.5 text-xs
			font-medium
			whitespace-nowrap text-white opacity-0 transition-opacity
			duration-150 group-hover:visible
			group-hover:opacity-100
		"
        role="tooltip"
    >
        {content}
        <span
            class="
				absolute h-0 w-0
				border-4 {arrowClasses[position]}
			"
        ></span>
    </div>
</div>
