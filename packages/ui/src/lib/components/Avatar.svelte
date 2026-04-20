<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    interface Props {
        name?: string;
        src?: string;
        size?: "xs" | "sm" | "md" | "lg" | "xl";
        status?: "online" | "offline" | "busy" | "away";
        class?: string;
    }

    let { name = "", src, size = "md", status, class: className = "" }: Props = $props();

    const initials = $derived(
        name
            .split(" ")
            .map((n) => n[0])
            .slice(0, 2)
            .join("")
            .toUpperCase(),
    );

    const sizeClasses: Record<string, string> = {
        xs: "h-6 w-6 text-xs",
        sm: "h-8 w-8 text-xs",
        md: "h-10 w-10 text-sm",
        lg: "h-12 w-12 text-base",
        xl: "h-16 w-16 text-lg",
    };

    const statusClasses: Record<string, string> = {
        online: "bg-success-500",
        offline: "bg-on-surface-faint",
        busy: "bg-danger-500",
        away: "bg-warning-500",
    };

    const statusSizes: Record<string, string> = {
        xs: "h-1.5 w-1.5",
        sm: "h-2 w-2",
        md: "h-2.5 w-2.5",
        lg: "h-3 w-3",
        xl: "h-4 w-4",
    };

    // Generate a consistent color based on name
    const colors = [
        "bg-wisp-500",
        "bg-aurora-500",
        "bg-success-500",
        "bg-warning-500",
        "bg-danger-500",
    ];

    const colorIndex = $derived(
        name.split("").reduce((acc, char) => acc + char.charCodeAt(0), 0) % colors.length,
    );
</script>

<div class="relative inline-flex {className}">
    {#if src}
        <img {src} alt={name} class="{sizeClasses[size]} rounded-full object-cover shadow-sm" />
    {:else}
        <div
            class="
				{sizeClasses[size]}
				{colors[colorIndex]}
				flex
				items-center justify-center rounded-full
				font-medium text-white shadow-sm
			"
        >
            {initials || "?"}
        </div>
    {/if}

    {#if status}
        <span
            class="
				absolute right-0 bottom-0
				{statusSizes[size]}
				{statusClasses[status]}
				rounded-full ring-2 ring-surface-raised
			"
        ></span>
    {/if}
</div>
