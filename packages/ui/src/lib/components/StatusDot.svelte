<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts" module>
    export type StatusDotTone = "primary" | "success" | "warning" | "danger" | "muted";
</script>

<script lang="ts">
    interface Props {
        tone?: StatusDotTone;
        /** Radar ping around the dot — use for "live" states only. */
        pulse?: boolean;
        size?: "sm" | "md" | "lg";
        class?: string;
    }

    let { tone = "primary", pulse = false, size = "sm", class: className = "" }: Props = $props();

    const TONES: Record<StatusDotTone, string> = {
        primary: "bg-primary",
        success: "bg-success-surface",
        warning: "bg-warning-surface",
        danger: "bg-danger-surface",
        muted: "bg-on-surface-faint",
    };
    const SIZES = { sm: "h-2 w-2", md: "h-2.5 w-2.5", lg: "h-3.5 w-3.5" };
</script>

<span class="relative flex shrink-0 {SIZES[size]} {className}" aria-hidden="true">
    {#if pulse}
        <span
            class="absolute inline-flex h-full w-full animate-ping rounded-full opacity-75 motion-reduce:animate-none {TONES[
                tone
            ]}"
        ></span>
    {/if}
    <span class="relative inline-flex h-full w-full rounded-full {TONES[tone]}"></span>
</span>
