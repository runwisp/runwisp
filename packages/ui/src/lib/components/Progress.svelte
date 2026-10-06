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
        /** Percent at which the bar turns warning / danger, overriding `variant`
         *  (e.g. `{ warning: 70, danger: 90 }` for CPU or memory). */
        thresholds?: { warning?: number; danger?: number };
        class?: string;
    }

    let {
        value,
        max = 100,
        variant = "default",
        size = "md",
        showLabel = false,
        label,
        thresholds,
        class: className = "",
    }: Props = $props();

    const percentage = $derived(Math.min(Math.max((value / max) * 100, 0), 100));

    const tone = $derived.by((): ProgressVariant => {
        if (thresholds?.danger !== undefined && percentage >= thresholds.danger) return "danger";
        if (thresholds?.warning !== undefined && percentage >= thresholds.warning) return "warning";
        return variant;
    });

    const variantClasses: Record<ProgressVariant, string> = {
        default: "bg-primary",
        success: "bg-success-surface",
        warning: "bg-warning-surface",
        danger: "bg-danger-surface",
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
                <span class="font-mono text-sm text-on-surface-muted">{label}</span>
            {/if}
            {#if showLabel}
                <span class="font-mono text-sm text-on-surface-muted"
                    >{Math.round(percentage)}%</span
                >
            {/if}
        </div>
    {/if}

    <div
        class="w-full {sizeClasses[size]} overflow-hidden rounded-full bg-surface-sunken"
        role="progressbar"
        aria-label={label}
        aria-valuenow={value}
        aria-valuemin={0}
        aria-valuemax={max}
    >
        <div
            class="{sizeClasses[size]} {variantClasses[
                tone
            ]} rounded-full transition-[width] duration-200"
            style="width: {percentage}%"
        ></div>
    </div>
</div>
