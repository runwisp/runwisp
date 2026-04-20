<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";
    import type { HTMLButtonAttributes } from "svelte/elements";

    type ButtonVariant = "primary" | "secondary" | "ghost" | "danger" | "success";
    type ButtonSize = "xs" | "sm" | "md" | "lg";

    interface Props extends HTMLButtonAttributes {
        variant?: ButtonVariant;
        size?: ButtonSize;
        fullWidth?: boolean;
        loading?: boolean;
        icon?: Snippet;
        iconRight?: Snippet;
        children?: Snippet;
    }

    let {
        variant = "primary",
        size = "md",
        fullWidth = false,
        loading = false,
        disabled = false,
        icon,
        iconRight,
        children,
        class: className = "",
        ...restProps
    }: Props = $props();

    const baseClasses = `
		group
		inline-flex items-center justify-center gap-2
		font-medium cursor-pointer select-none
		border border-transparent
		focus-visible:outline-none focus-visible:ring-4 focus-visible:ring-ring/10 focus-visible:border-ring
		disabled:opacity-50 disabled:cursor-not-allowed
		transition-all duration-normal ease-out
        active:scale-[0.98]
	`;

    const variantClasses: Record<ButtonVariant, string> = {
        primary: `
			bg-primary text-on-primary shadow-sm shadow-primary/20
			hover:bg-primary-hover hover:shadow-md hover:shadow-primary/30
			active:bg-primary-active
		`,
        secondary: `
			bg-surface-raised text-on-surface-muted border-outline shadow-sm
			hover:bg-surface-sunken hover:text-on-surface hover:border-outline-hover
			active:bg-surface-sunken
		`,
        ghost: `
			bg-transparent text-on-surface-muted
			hover:bg-surface-sunken hover:text-on-surface
			active:bg-surface-sunken
		`,
        danger: `
			bg-danger-surface text-on-danger shadow-sm shadow-danger-surface/20
			hover:bg-danger-hover hover:shadow-md hover:shadow-danger-surface/30
			active:bg-danger-active
		`,
        success: `
			bg-success-surface text-on-success shadow-sm shadow-success-surface/20
			hover:bg-success-hover hover:shadow-md hover:shadow-success-surface/30
			active:bg-success-surface
		`,
    };

    const sizeClasses: Record<ButtonSize, string> = {
        xs: "text-xs px-2 py-1 rounded-md",
        sm: "text-sm px-3 py-1.5 rounded-lg",
        md: "text-sm px-4 py-2 rounded-lg",
        lg: "text-base px-6 py-3 rounded-xl",
    };
</script>

<button
    class="{baseClasses} {variantClasses[variant]} {sizeClasses[size]} {fullWidth
        ? 'w-full'
        : ''} {className}"
    disabled={disabled || loading}
    {...restProps}
>
    {#if loading}
        <svg class="h-4 w-4 animate-spin" viewBox="0 0 24 24" fill="none">
            <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"
            ></circle>
            <path
                class="opacity-75"
                fill="currentColor"
                d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
            ></path>
        </svg>
    {:else if icon}
        <span class="shrink-0 transition-transform group-active:scale-95">
            {@render icon()}
        </span>
    {/if}
    {#if children}
        {@render children()}
    {/if}
    {#if iconRight && !loading}
        <span class="shrink-0 transition-transform group-active:scale-95">
            {@render iconRight()}
        </span>
    {/if}
</button>
