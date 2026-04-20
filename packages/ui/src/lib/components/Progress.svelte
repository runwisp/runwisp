<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    type ProgressVariant = "default" | "success" | "warning" | "danger";
    type ProgressSize = "sm" | "md" | "lg";

    interface Props {
        value: number;
        max?: number;
        variant?: ProgressVariant;
        size?: ProgressSize;
        showLabel?: boolean;
        label?: string;
        class?: string;
    }

    let {
        value,
        max = 100,
        variant = "default",
        size = "md",
        showLabel = false,
        label,
        class: className = "",
    }: Props = $props();

    const percentage = $derived(Math.min(Math.max((value / max) * 100, 0), 100));

    const variantClasses: Record<ProgressVariant, string> = {
        default: "bg-wisp-500",
        success: "bg-success-500",
        warning: "bg-warning-500",
        danger: "bg-danger-500",
    };

    const sizeClasses: Record<ProgressSize, string> = {
        sm: "h-1",
        md: "h-2",
        lg: "h-3",
    };
</script>

<div class={className}>
    {#if showLabel || label}
        <div class="mb-1.5 flex items-center justify-between">
            {#if label}
                <span class="text-sm font-medium text-on-surface-muted">{label}</span>
            {/if}
            {#if showLabel}
                <span class="text-sm text-on-surface-muted">{Math.round(percentage)}%</span>
            {/if}
        </div>
    {/if}

    <div class="w-full {sizeClasses[size]} overflow-hidden rounded-full bg-outline">
        <div
            class="{sizeClasses[size]} {variantClasses[
                variant
            ]} rounded-full transition-all duration-300"
            style="width: {percentage}%"
            role="progressbar"
            aria-valuenow={value}
            aria-valuemin={0}
            aria-valuemax={max}
        ></div>
    </div>
</div>
