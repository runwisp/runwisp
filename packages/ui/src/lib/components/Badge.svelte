<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";

    type BadgeVariant = "default" | "primary" | "success" | "warning" | "danger" | "info";
    type BadgeSize = "sm" | "md" | "lg";

    interface Props {
        variant?: BadgeVariant;
        size?: BadgeSize;
        dot?: boolean;
        loading?: boolean;
        outline?: boolean;
        children?: Snippet;
        class?: string;
    }

    let {
        variant = "default",
        size = "md",
        dot = false,
        loading = false,
        outline = false,
        children,
        class: className = "",
    }: Props = $props();

    const baseClasses = `
		inline-flex items-center gap-1.5
		font-medium rounded-full
		whitespace-nowrap
	`;

    const variantClasses: Record<BadgeVariant, { solid: string; outline: string }> = {
        default: {
            solid: "bg-surface-sunken text-on-surface-muted",
            outline: "bg-transparent border border-outline-hover text-on-surface-muted",
        },
        primary: {
            solid: "bg-primary-soft text-primary-soft-text",
            outline: "bg-transparent border border-wisp-300 text-primary-soft-text",
        },
        success: {
            solid: "bg-success-soft text-success-soft-text",
            outline: "bg-transparent border border-success-300 text-success-soft-text",
        },
        warning: {
            solid: "bg-warning-soft text-warning-soft-text",
            outline: "bg-transparent border border-warning-400 text-warning-soft-text",
        },
        danger: {
            solid: "bg-danger-soft text-danger-soft-text",
            outline: "bg-transparent border border-danger-300 text-danger-soft-text",
        },
        info: {
            solid: "bg-info-soft text-info-soft-text",
            outline: "bg-transparent border border-aurora-300 text-info-soft-text",
        },
    };

    const sizeClasses: Record<BadgeSize, string> = {
        sm: "text-2xs px-1.5 py-0.5",
        md: "text-xs px-2 py-0.5",
        lg: "text-sm px-2.5 py-1",
    };

    const dotColors: Record<BadgeVariant, string> = {
        default: "bg-mist-500",
        primary: "bg-primary",
        success: "bg-success-surface",
        warning: "bg-warning-surface",
        danger: "bg-danger-surface",
        info: "bg-info",
    };

    const indicatorTextColors: Record<BadgeVariant, string> = {
        default: "text-on-surface-muted",
        primary: "text-primary",
        success: "text-success-soft-text",
        warning: "text-warning-soft-text",
        danger: "text-danger-soft-text",
        info: "text-info",
    };

    const spinnerSizeClasses: Record<BadgeSize, string> = {
        sm: "h-3 w-3",
        md: "h-3.5 w-3.5",
        lg: "h-4 w-4",
    };
</script>

<span
    class="{baseClasses} {outline
        ? variantClasses[variant].outline
        : variantClasses[variant].solid} {sizeClasses[size]} {className}"
>
    {#if dot}
        {#if loading}
            <svg
                class="{spinnerSizeClasses[size]} animate-spin {indicatorTextColors[variant]}"
                viewBox="0 0 24 24"
                fill="none"
            >
                <circle
                    class="opacity-25"
                    cx="12"
                    cy="12"
                    r="10"
                    stroke="currentColor"
                    stroke-width="4"
                ></circle>
                <path
                    class="opacity-75"
                    fill="currentColor"
                    d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
                ></path>
            </svg>
        {:else}
            <span class="h-1.5 w-1.5 rounded-full {dotColors[variant]}"></span>
        {/if}
    {/if}
    {#if children}
        {@render children()}
    {/if}
</span>
