<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { Calendar, Ban, Check, Clock, Pause, X } from "@lucide/svelte";
    import type { Component } from "svelte";
    import { fade } from "svelte/transition";
    import Spinner from "./Spinner.svelte";

    type Status =
        "running" | "success" | "failed" | "pending" | "paused" | "scheduled" | "cancelled";
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

    // `icon` is undefined for "running", which shows the spinner.
    const STATUS_CONFIG: Record<
        Status,
        {
            label: string;
            classes: string;
            iconColor: string;
            icon: Component | undefined;
            strokeWidth: number;
        }
    > = {
        running: {
            label: "Running",
            classes: "bg-info-soft text-info-soft-text border-info-soft-border",
            iconColor: "text-info-surface",
            icon: undefined,
            strokeWidth: 3,
        },
        success: {
            label: "Success",
            classes: "bg-success-soft text-success-soft-text border-success-soft-border",
            iconColor: "text-success-surface",
            icon: Check,
            strokeWidth: 3,
        },
        failed: {
            label: "Failed",
            classes: "bg-danger-soft text-danger-soft-text border-danger-soft-border",
            iconColor: "text-danger-surface",
            icon: X,
            strokeWidth: 3,
        },
        pending: {
            label: "Pending",
            classes: "bg-warning-soft text-warning-soft-text border-warning-soft-border",
            iconColor: "text-warning-surface",
            icon: Clock,
            strokeWidth: 3,
        },
        paused: {
            label: "Paused",
            classes: "bg-surface-sunken text-on-surface-muted border-outline",
            iconColor: "text-on-surface-faint",
            icon: Pause,
            strokeWidth: 0,
        },
        scheduled: {
            label: "Scheduled",
            classes: "bg-primary-soft text-primary-soft-text border-primary-soft-border",
            iconColor: "text-primary",
            icon: Calendar,
            strokeWidth: 2.5,
        },
        cancelled: {
            label: "Cancelled",
            classes: "bg-surface-sunken text-on-surface-faint border-outline opacity-75",
            iconColor: "text-on-surface-faint",
            icon: Ban,
            strokeWidth: 2.5,
        },
    };

    const SIZE_CONFIG: Record<Size, { classes: string; iconSize: number }> = {
        sm: { classes: "px-2 py-0.5 text-2xs gap-1.5", iconSize: 10 },
        md: { classes: "px-2.5 py-0.5 text-xs gap-1.5", iconSize: 12 },
        lg: { classes: "px-3 py-1 text-sm gap-2", iconSize: 14 },
    };

    const config = $derived(STATUS_CONFIG[status]);
    const sizeConfig = $derived(SIZE_CONFIG[size]);
</script>

<div
    class="inline-flex items-center justify-center rounded-full border font-mono tracking-wide {config.classes} {sizeConfig.classes} {className}"
    role="status"
    in:fade={{ duration: 150 }}
>
    <span class="flex shrink-0 items-center justify-center {config.iconColor}">
        {#if !config.icon || pulse}
            <Spinner size={sizeConfig.iconSize} label="" />
        {:else}
            {@const Icon = config.icon}
            <Icon
                size={sizeConfig.iconSize}
                strokeWidth={config.strokeWidth}
                fill={status === "paused" ? "currentColor" : "none"}
            />
        {/if}
    </span>

    {#if showLabel}
        <span class="truncate">{label ?? config.label}</span>
    {/if}
</div>
