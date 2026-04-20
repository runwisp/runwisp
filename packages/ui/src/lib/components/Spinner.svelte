<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    type SpinnerSize = "xs" | "sm" | "md" | "lg";

    interface Props {
        size?: SpinnerSize;
        color?: string;
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

<div role="status" class="inline-flex {className}">
    <svg
        class="animate-spin {sizeClasses[size]}"
        viewBox="0 0 24 24"
        fill="none"
        xmlns="http://www.w3.org/2000/svg"
    >
        <circle class="opacity-25" cx="12" cy="12" r="10" stroke={color} stroke-width="4"></circle>
        <path
            class="opacity-75"
            fill={color}
            d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
        ></path>
    </svg>
    <span class="sr-only">{label}</span>
</div>
