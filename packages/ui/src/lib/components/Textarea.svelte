<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { HTMLTextareaAttributes } from "svelte/elements";
    import { CircleAlert } from "@lucide/svelte";

    interface Props extends HTMLTextareaAttributes {
        label?: string;
        hint?: string;
        error?: string;
        resize?: "none" | "vertical" | "horizontal" | "both";
        class?: string;
        value?: string;
    }

    let {
        label,
        hint,
        error,
        disabled = false,
        resize = "vertical",
        class: className = "",
        value = $bindable(""),
        id,
        ...restProps
    }: Props = $props();

    const resizeClasses: Record<string, string> = {
        none: "resize-none",
        vertical: "resize-y",
        horizontal: "resize-x",
        both: "resize",
    };

    const textareaClasses = `
		w-full px-3.5 py-2.5 rounded-lg border
		text-sm bg-surface-raised text-on-surface placeholder:text-on-surface-faint
		focus:outline-none focus:ring-2 focus:ring-offset-0
		disabled:bg-surface-sunken disabled:text-on-surface-muted disabled:cursor-not-allowed
		transition-colors duration-fast
	`;

    const normalBorder =
        "border-outline hover:border-outline-hover focus:border-ring focus:ring-ring/20";
    const errorBorder = "border-danger-400 focus:border-danger-500 focus:ring-danger-500/20";
</script>

<div class="space-y-1.5 {className}">
    {#if label}
        <label class="block text-sm font-medium text-on-surface-muted" for={id}>
            {label}
        </label>
    {/if}

    <div class="relative">
        <textarea
            class="
					{textareaClasses}
					{error ? errorBorder : normalBorder}
					{resizeClasses[resize]}
				"
            {disabled}
            {id}
            bind:value
            {...restProps}
        ></textarea>

        {#if error}
            <div class="pointer-events-none absolute top-3 right-3 text-danger-soft-text">
                <CircleAlert size={18} />
            </div>
        {/if}
    </div>

    {#if error}
        <p class="text-sm text-danger-soft-text">{error}</p>
    {:else if hint}
        <p class="text-sm text-on-surface-muted">{hint}</p>
    {/if}
</div>
