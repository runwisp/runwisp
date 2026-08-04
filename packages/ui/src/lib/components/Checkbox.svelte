<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { HTMLInputAttributes } from "svelte/elements";
    import { Check } from "@lucide/svelte";

    type CheckboxSize = "sm" | "md" | "lg";

    interface Props extends Omit<HTMLInputAttributes, "size" | "type"> {
        size?: CheckboxSize;
        label?: string;
        description?: string;
        checked?: boolean;
        indeterminate?: boolean;
        class?: string;
    }

    let {
        size = "md",
        label,
        description,
        checked = $bindable(false),
        indeterminate = false,
        disabled = false,
        class: className = "",
        ...restProps
    }: Props = $props();

    const sizeClasses: Record<CheckboxSize, { box: string; icon: number; text: string }> = {
        sm: { box: "h-4 w-4", icon: 12, text: "text-sm" },
        md: { box: "h-5 w-5", icon: 14, text: "text-sm" },
        lg: { box: "h-6 w-6", icon: 16, text: "text-base" },
    };
</script>

<label
    class="inline-flex items-start gap-3 {disabled
        ? 'cursor-not-allowed opacity-50'
        : 'cursor-pointer'} {className}"
>
    <div class="relative flex shrink-0 items-center justify-center">
        <input
            type="checkbox"
            bind:checked
            {disabled}
            class="
				peer {sizeClasses[size].box}
				cursor-pointer appearance-none rounded-[3px]
				border border-outline
				bg-surface-raised checked:border-primary
				checked:bg-primary hover:border-outline-hover checked:hover:border-primary-hover checked:hover:bg-primary-hover
				focus:ring-2 focus:ring-ring focus:ring-offset-2
				focus:outline-none
				disabled:cursor-not-allowed disabled:hover:border-outline
				disabled:checked:hover:bg-primary
			"
            {...restProps}
        />
        <div
            class="pointer-events-none absolute text-on-primary opacity-0 peer-checked:opacity-100"
        >
            {#if indeterminate}
                <svg
                    width={sizeClasses[size].icon}
                    height={sizeClasses[size].icon}
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    stroke-width="3"
                >
                    <line x1="5" y1="12" x2="19" y2="12" />
                </svg>
            {:else}
                <Check size={sizeClasses[size].icon} strokeWidth={3} />
            {/if}
        </div>
    </div>

    {#if label || description}
        <div class="flex flex-col gap-0.5">
            {#if label}
                <span class="{sizeClasses[size].text} font-mono font-medium text-on-surface"
                    >{label}</span
                >
            {/if}
            {#if description}
                <span class="text-sm text-on-surface-muted">{description}</span>
            {/if}
        </div>
    {/if}
</label>
