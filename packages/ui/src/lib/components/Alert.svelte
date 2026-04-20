<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";
    import { Info, CircleCheckBig, TriangleAlert, CircleX, X } from "@lucide/svelte";

    type AlertVariant = "info" | "success" | "warning" | "danger";

    interface Props {
        variant?: AlertVariant;
        title?: string;
        dismissible?: boolean;
        onDismiss?: () => void;
        icon?: Snippet;
        actions?: Snippet;
        children?: Snippet;
        class?: string;
    }

    let {
        variant = "info",
        title,
        dismissible = false,
        onDismiss,
        icon,
        actions,
        children,
        class: className = "",
    }: Props = $props();

    const variantConfig: Record<
        AlertVariant,
        { bg: string; border: string; icon: typeof Info; iconColor: string }
    > = {
        info: {
            bg: "bg-info-soft",
            border: "border-aurora-200",
            icon: Info,
            iconColor: "text-info",
        },
        success: {
            bg: "bg-success-soft",
            border: "border-success-200",
            icon: CircleCheckBig,
            iconColor: "text-success-surface",
        },
        warning: {
            bg: "bg-warning-soft",
            border: "border-warning-200",
            icon: TriangleAlert,
            iconColor: "text-warning-surface",
        },
        danger: {
            bg: "bg-danger-soft",
            border: "border-danger-200",
            icon: CircleX,
            iconColor: "text-danger-surface",
        },
    };

    const config = $derived(variantConfig[variant]);
</script>

<div
    class="
		flex gap-3 rounded-xl border p-4
		{config.bg} {config.border}
		{className}
	"
    role="alert"
>
    <div class="shrink-0">
        {#if icon}
            {@render icon()}
        {:else}
            {@const IconComponent = config.icon}
            <IconComponent size={20} class={config.iconColor} />
        {/if}
    </div>

    <div class="min-w-0 flex-1">
        {#if title}
            <h4 class="mb-1 text-sm font-semibold text-on-surface">{title}</h4>
        {/if}
        {#if children}
            <div class="text-sm text-on-surface-muted">
                {@render children()}
            </div>
        {/if}
        {#if actions}
            <div class="mt-3">
                {@render actions()}
            </div>
        {/if}
    </div>

    {#if dismissible}
        <button
            onclick={onDismiss}
            class="shrink-0 rounded-lg p-1 text-on-surface-faint transition-colors hover:bg-surface-raised/50 hover:text-on-surface-muted"
            aria-label="Dismiss"
        >
            <X size={16} />
        </button>
    {/if}
</div>
