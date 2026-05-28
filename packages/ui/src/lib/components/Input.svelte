<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";
    import type { HTMLInputAttributes } from "svelte/elements";
    import { CircleAlert } from "@lucide/svelte";

    type InputSize = "sm" | "md" | "lg";

    interface Props extends Omit<HTMLInputAttributes, "size"> {
        size?: InputSize;
        label?: string;
        hint?: string;
        error?: string | undefined;
        leadingIcon?: Snippet;
        trailingIcon?: Snippet;
        class?: string;
    }

    let {
        size = "md",
        value = $bindable(),
        id,
        label,
        hint,
        error = $bindable(),
        leadingIcon,
        trailingIcon,
        disabled = false,
        class: className = "",
        oninput,
        ...restProps
    }: Props = $props();

    const generatedId = `input-${Math.random().toString(36).slice(2, 10)}`;
    const inputId = $derived(id ?? generatedId);

    const sizeClasses: Record<InputSize, string> = {
        sm: "text-sm px-3 py-1.5",
        md: "text-sm px-3.5 py-2",
        lg: "text-base px-4 py-2.5",
    };

    const inputClasses = `
		w-full rounded-lg border
		bg-surface-raised text-on-surface placeholder:text-on-surface-faint
		focus:outline-none focus:ring-4 focus:ring-ring/10 focus:border-ring
		disabled:bg-surface-sunken disabled:text-on-surface-muted disabled:cursor-not-allowed
		transition-all duration-normal ease-in-out
	`;

    const normalBorder = "border-outline hover:border-outline-hover shadow-sm";
    const errorBorder = "border-danger-400 focus:border-danger-500 focus:ring-danger-500/20";
</script>

<div class="space-y-1.5 {className}">
    {#if label}
        <label for={inputId} class="block text-sm font-medium text-on-surface-muted">
            {label}
        </label>
    {/if}

    <div class="group relative">
        {#if leadingIcon}
            <div
                class="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-on-surface-faint transition-colors group-focus-within:text-on-surface-muted"
            >
                {@render leadingIcon()}
            </div>
        {/if}

        <input
            id={inputId}
            class="
				{inputClasses}
				{sizeClasses[size]}
				{error ? errorBorder : normalBorder}
				{leadingIcon ? 'pl-10' : ''}
				{trailingIcon || error ? 'pr-10' : ''}
			"
            {disabled}
            bind:value
            aria-invalid={error ? "true" : undefined}
            oninput={(e) => {
                if (error) error = undefined;
                oninput?.(e);
            }}
            {...restProps}
        />

        {#if error}
            <div
                class="pointer-events-none absolute top-1/2 right-3 -translate-y-1/2 text-danger-soft-text"
            >
                <CircleAlert size={18} />
            </div>
        {:else if trailingIcon}
            <div
                class="pointer-events-none absolute top-1/2 right-3 -translate-y-1/2 text-on-surface-faint transition-colors group-focus-within:text-on-surface-muted"
            >
                {@render trailingIcon()}
            </div>
        {/if}
    </div>

    {#if error}
        <p
            class="animate-in slide-in-from-top-1 fade-in text-sm text-danger-soft-text duration-200"
        >
            {error}
        </p>
    {:else if hint}
        <p class="text-sm text-on-surface-muted">{hint}</p>
    {/if}
</div>
