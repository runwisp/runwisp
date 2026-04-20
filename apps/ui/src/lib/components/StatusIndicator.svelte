<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { fade } from "svelte/transition";

    type Status =
        | "running"
        | "success"
        | "failed"
        | "pending"
        | "paused"
        | "scheduled"
        | "cancelled";
    type Size = "sm" | "md" | "lg";

    interface Props {
        status: Status;
        size?: Size;
        showLabel?: boolean;
        pulse?: boolean;
        label?: string;
        class?: string;
    }

    let {
        status,
        size = "md",
        showLabel = true,
        pulse = false,
        label = undefined,
        class: className = "",
    }: Props = $props();

    // Status configuration lookup
    const STATUS_CONFIG: Record<Status, { label: string; classes: string; iconColor: string }> = {
        running: {
            label: "Running",
            classes:
                "bg-aurora-50 text-aurora-700 border-aurora-200 shadow-glow-sm ring-1 ring-aurora-100",
            iconColor: "text-aurora-600",
        },
        success: {
            label: "Success",
            classes: "bg-success-50 text-success-700 border-success-200 ring-1 ring-success-100/50",
            iconColor: "text-success-600",
        },
        failed: {
            label: "Failed",
            classes: "bg-danger-50 text-danger-700 border-danger-200 ring-1 ring-danger-100/50",
            iconColor: "text-danger-600",
        },
        pending: {
            label: "Pending",
            classes: "bg-warning-50 text-warning-700 border-warning-200",
            iconColor: "text-warning-600",
        },
        paused: {
            label: "Paused",
            classes: "bg-mist-100 text-mist-600 border-mist-200",
            iconColor: "text-mist-500",
        },
        scheduled: {
            label: "Scheduled",
            classes: "bg-wisp-50 text-wisp-700 border-wisp-200",
            iconColor: "text-wisp-600",
        },
        cancelled: {
            label: "Cancelled",
            classes: "bg-mist-50 text-mist-500 border-mist-200 opacity-75",
            iconColor: "text-mist-400",
        },
    };

    // Size configuration lookup
    const SIZE_CONFIG: Record<Size, { classes: string; iconSize: number }> = {
        sm: { classes: "px-2 py-0.5 text-2xs gap-1.5", iconSize: 10 },
        md: { classes: "px-2.5 py-0.5 text-xs gap-1.5", iconSize: 12 },
        lg: { classes: "px-3 py-1 text-sm gap-2", iconSize: 14 },
    };

    const config = $derived(STATUS_CONFIG[status]);
    const sizeConfig = $derived(SIZE_CONFIG[size]);
</script>

<div
    class="inline-flex items-center justify-center rounded-full border font-medium transition-all duration-200 {config.classes} {sizeConfig.classes} {className}"
    role="status"
    in:fade={{ duration: 150 }}
>
    <!-- Icon Container -->
    <span class="flex shrink-0 items-center justify-center {config.iconColor}">
        {#if status === "running" || pulse}
            <svg
                width={sizeConfig.iconSize}
                height={sizeConfig.iconSize}
                viewBox="0 0 24 24"
                fill="none"
                class="animate-spin"
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
        {:else if status === "success"}
            <svg
                width={sizeConfig.iconSize}
                height={sizeConfig.iconSize}
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                stroke-width="3"
                stroke-linecap="round"
                stroke-linejoin="round"
            >
                <polyline points="20 6 9 17 4 12"></polyline>
            </svg>
        {:else if status === "failed"}
            <svg
                width={sizeConfig.iconSize}
                height={sizeConfig.iconSize}
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                stroke-width="3"
                stroke-linecap="round"
                stroke-linejoin="round"
            >
                <line x1="18" y1="6" x2="6" y2="18"></line>
                <line x1="6" y1="6" x2="18" y2="18"></line>
            </svg>
        {:else if status === "pending"}
            <svg
                width={sizeConfig.iconSize}
                height={sizeConfig.iconSize}
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                stroke-width="3"
                stroke-linecap="round"
                stroke-linejoin="round"
            >
                <circle cx="12" cy="12" r="10"></circle>
                <polyline points="12 6 12 12 16 14"></polyline>
            </svg>
        {:else if status === "scheduled"}
            <svg
                width={sizeConfig.iconSize}
                height={sizeConfig.iconSize}
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                stroke-width="2.5"
                stroke-linecap="round"
                stroke-linejoin="round"
            >
                <rect x="3" y="4" width="18" height="18" rx="2" ry="2"></rect>
                <line x1="16" y1="2" x2="16" y2="6"></line>
                <line x1="8" y1="2" x2="8" y2="6"></line>
                <line x1="3" y1="10" x2="21" y2="10"></line>
            </svg>
        {:else if status === "paused"}
            <svg
                width={sizeConfig.iconSize}
                height={sizeConfig.iconSize}
                viewBox="0 0 24 24"
                fill="currentColor"
            >
                <rect x="6" y="4" width="4" height="16" rx="1" />
                <rect x="14" y="4" width="4" height="16" rx="1" />
            </svg>
        {:else if status === "cancelled"}
            <svg
                width={sizeConfig.iconSize}
                height={sizeConfig.iconSize}
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                stroke-width="2.5"
                stroke-linecap="round"
                stroke-linejoin="round"
            >
                <circle cx="12" cy="12" r="10"></circle>
                <line x1="4.93" y1="4.93" x2="19.07" y2="19.07"></line>
            </svg>
        {/if}
    </span>

    {#if showLabel}
        <span class="truncate">{label ?? config.label}</span>
    {/if}
</div>
