<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    type SpinnerSize = "xs" | "sm" | "md" | "lg";

    interface Props {
        /** A preset, or an exact pixel size. */
        size?: SpinnerSize | number;
        color?: string;
        /** Screen-reader text. An empty label drops the status role, for spinners inside a labelled control. */
        label?: string;
        class?: string;
    }

    let {
        size = "md",
        color = "currentColor",
        label = "Loading",
        class: className = "",
    }: Props = $props();

    const sizeClasses: Record<SpinnerSize, string> = {
        xs: "h-3 w-3",
        sm: "h-4 w-4",
        md: "h-6 w-6",
        lg: "h-8 w-8",
    };
</script>

<span role={label ? "status" : undefined} class="inline-flex {className}">
    <svg
        class="animate-spin {typeof size === 'number' ? '' : sizeClasses[size]}"
        width={typeof size === "number" ? size : undefined}
        height={typeof size === "number" ? size : undefined}
        viewBox="0 0 24 24"
        fill="none"
        xmlns="http://www.w3.org/2000/svg"
        aria-hidden="true"
    >
        <circle class="opacity-25" cx="12" cy="12" r="10" stroke={color} stroke-width="4"></circle>
        <path
            class="opacity-75"
            fill={color}
            d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
        ></path>
    </svg>
    {#if label}<span class="sr-only">{label}</span>{/if}
</span>
