<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    type KbdSize = "xs" | "sm";
    /** `faint` sits quietly inside an input; `console` rides dark terminal chrome and inherits its text color. */
    type KbdTone = "default" | "faint" | "console";

    interface Props {
        keys: string | string[];
        size?: KbdSize;
        tone?: KbdTone;
        class?: string;
    }

    let { keys, size = "sm", tone = "default", class: className = "" }: Props = $props();

    const keyList = $derived(Array.isArray(keys) ? keys : [keys]);

    const sizeClasses: Record<KbdSize, string> = {
        xs: "px-1.5 text-2xs",
        sm: "min-w-[1.5em] px-1.5 text-xs",
    };

    // Vertical padding lives with the tone: console keys sit inline in toolbar
    // text and must not grow the row.
    const toneClasses: Record<KbdTone, string> = {
        default:
            "py-0.5 font-medium bg-surface-sunken text-on-surface-muted border-outline shadow-sm",
        faint: "py-0.5 font-medium bg-surface-raised text-on-surface-faint border-outline-faint shadow-sm",
        console: "tracking-wide border-[var(--rw-con-gutter)]",
    };
</script>

<span class="inline-flex items-center gap-1 {className}">
    {#each keyList as key, i (key)}
        {#if i > 0}
            <span class="text-xs text-on-surface-faint select-none">+</span>
        {/if}
        <kbd
            class="inline-flex items-center justify-center rounded-[3px] border font-mono {sizeClasses[
                size
            ]} {toneClasses[tone]}">{key}</kbd
        >
    {/each}
</span>
