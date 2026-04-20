<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";

    interface Props {
        padding?: "none" | "sm" | "md" | "lg";
        hover?: boolean;
        border?: boolean;
        shadow?: "none" | "sm" | "md" | "lg";
        header?: Snippet;
        footer?: Snippet;
        children?: Snippet;
        class?: string;
    }

    let {
        padding = "md",
        hover = false,
        border = true,
        shadow = "sm",
        header,
        footer,
        children,
        class: className = "",
    }: Props = $props();

    const paddingClasses: Record<string, string> = {
        none: "",
        sm: "p-3",
        md: "p-4",
        lg: "p-6",
    };

    const shadowClasses: Record<string, string> = {
        none: "",
        sm: "shadow-sm",
        md: "shadow-md",
        lg: "shadow-lg",
    };
</script>

<div
    class="
		overflow-hidden rounded-xl bg-surface-raised
		{border ? 'border border-outline' : ''}
		{shadowClasses[shadow]}
		{hover ? 'duration-fast transition-shadow hover:border-outline-hover hover:shadow-md' : ''}
		{className}
	"
>
    {#if header}
        <div class="border-b border-outline-faint bg-surface-sunken/50 px-4 py-3">
            {@render header()}
        </div>
    {/if}

    <div class={paddingClasses[padding]}>
        {#if children}
            {@render children()}
        {/if}
    </div>

    {#if footer}
        <div class="border-t border-outline-faint bg-surface-sunken/50 px-4 py-3">
            {@render footer()}
        </div>
    {/if}
</div>
