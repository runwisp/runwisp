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

    const generatedId = $props.id();
    const inputId = $derived(id ?? generatedId);

    const sizeClasses: Record<InputSize, string> = {
        sm: "text-sm px-3 py-1.5",
        md: "text-sm px-3.5 py-2",
        lg: "text-base px-4 py-2.5",
    };

    const inputClasses = `
		w-full rounded-[3px] border font-mono
		bg-surface-raised text-on-surface placeholder:text-on-surface-faint
		focus:outline-none focus:border-ring focus:ring-2 focus:ring-ring focus:ring-offset-2
		disabled:bg-surface-sunken disabled:text-on-surface-muted disabled:cursor-not-allowed
			`;

    const normalBorder = "border-outline hover:border-outline-hover shadow-sm";
    const errorBorder =
        "border-danger-surface focus:border-danger-surface focus:ring-danger-surface";
</script>

<div class="space-y-1.5 {className}">
    {#if label}
        <label for={inputId} class="block font-mono text-xs font-medium text-on-surface-muted">
            {label}
        </label>
    {/if}

    <div class="group relative">
        {#if leadingIcon}
            <div
                class="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-on-surface-faint group-focus-within:text-on-surface-muted"
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
                class="pointer-events-none absolute top-1/2 right-3 -translate-y-1/2 text-on-surface-faint group-focus-within:text-on-surface-muted"
            >
                {@render trailingIcon()}
            </div>
        {/if}
    </div>

    {#if error}
        <p class="font-sans text-xs text-danger-soft-text">
            {error}
        </p>
    {:else if hint}
        <p class="font-sans text-xs text-on-surface-muted">{hint}</p>
    {/if}
</div>
