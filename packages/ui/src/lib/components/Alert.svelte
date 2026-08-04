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
            bg: "bg-info-soft text-info-soft-text",
            border: "border-info-soft-border",
            icon: Info,
            iconColor: "text-info-soft-text",
        },
        success: {
            bg: "bg-success-soft text-success-soft-text",
            border: "border-success-soft-border",
            icon: CircleCheckBig,
            iconColor: "text-success-soft-text",
        },
        warning: {
            bg: "bg-warning-soft text-warning-soft-text",
            border: "border-warning-soft-border",
            icon: TriangleAlert,
            iconColor: "text-warning-soft-text",
        },
        danger: {
            bg: "bg-danger-soft text-danger-soft-text",
            border: "border-danger-soft-border",
            icon: CircleX,
            iconColor: "text-danger-soft-text",
        },
    };

    const config = $derived(variantConfig[variant]);
</script>

<div
    class="
		flex gap-3 rounded-[4px] border p-4
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
            <h4 class="mb-1 font-mono text-sm font-semibold text-on-surface">{title}</h4>
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
            class="shrink-0 rounded-[3px] p-1 text-on-surface-faint hover:bg-surface-raised hover:text-on-surface-muted"
            aria-label="Dismiss"
        >
            <X size={16} />
        </button>
    {/if}
</div>
