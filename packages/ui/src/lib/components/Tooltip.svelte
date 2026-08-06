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
        /** Wrap long content across lines instead of one nowrap line. */
        wide?: boolean;
    }

    let {
        content,
        position = "top",
        children,
        class: className = "",
        wide = false,
    }: Props = $props();

    const positionClasses: Record<TooltipPosition, string> = {
        top: "bottom-full left-1/2 -translate-x-1/2 mb-2",
        bottom: "top-full left-1/2 -translate-x-1/2 mt-2",
        left: "right-full top-1/2 -translate-y-1/2 mr-2",
        right: "left-full top-1/2 -translate-y-1/2 ml-2",
    };

    const arrowClasses: Record<TooltipPosition, string> = {
        top: "top-full left-1/2 -translate-x-1/2 border-t-outline border-x-transparent border-b-transparent",
        bottom: "bottom-full left-1/2 -translate-x-1/2 border-b-outline border-x-transparent border-t-transparent",
        left: "left-full top-1/2 -translate-y-1/2 border-l-outline border-y-transparent border-r-transparent",
        right: "right-full top-1/2 -translate-y-1/2 border-r-outline border-y-transparent border-l-transparent",
    };
</script>

<div class="group relative inline-block {className}">
    {@render children()}

    <!-- Hover intent: 100ms before it appears, so sweeping the pointer across a
         dense row doesn't flash a trail of bubbles. Still no fade — `duration-0`
         keeps the appearance instant once the delay is served, per DESIGN.md. -->
    <div
        class="
			absolute z-50 {positionClasses[position]}
			pointer-events-none invisible rounded-[3px]
			border border-outline bg-surface-overlay px-2.5 py-1.5 font-sans
			text-xs
			{wide ? 'w-max max-w-xs whitespace-normal' : 'whitespace-nowrap'}
			text-on-surface opacity-0 transition-[opacity,visibility]
			delay-100 duration-0 group-hover:visible group-hover:opacity-100
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
