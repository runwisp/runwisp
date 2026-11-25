<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    interface Props {
        checked?: boolean;
        disabled?: boolean;
        size?: "sm" | "md" | "lg";
        label?: string;
        description?: string;
        onchange?: (checked: boolean) => void;
        class?: string;
    }

    let {
        checked = $bindable(),
        disabled = false,
        size = "md",
        label,
        description,
        onchange,
        class: className = "",
    }: Props = $props();

    function toggle() {
        if (disabled) return;
        checked = !checked;
        onchange?.(checked);
    }

    // Exact pixel geometry: track = 2 * thumb + 4 (1px border + 1px gap each side),
    // so the thumb travels exactly one thumb width.
    const sizeClasses: Record<
        "sm" | "md" | "lg",
        { track: string; thumb: string; translate: string; grip: string; text: string }
    > = {
        sm: {
            track: "h-[18px] w-8",
            thumb: "size-3.5",
            translate: "translate-x-3.5",
            grip: "h-1.5",
            text: "text-sm",
        },
        md: {
            track: "h-[22px] w-10",
            thumb: "size-[18px]",
            translate: "translate-x-[18px]",
            grip: "h-2",
            text: "text-sm",
        },
        lg: {
            track: "h-7 w-[52px]",
            thumb: "size-6",
            translate: "translate-x-6",
            grip: "h-2.5",
            text: "text-base",
        },
    };

    const sizeConfig = $derived(sizeClasses[size]);
</script>

<label
    class="inline-flex items-start gap-3 {disabled
        ? 'cursor-not-allowed opacity-50'
        : 'cursor-pointer'} {className}"
>
    <button
        type="button"
        role="switch"
        aria-checked={checked}
        aria-label={label ?? description ?? "Toggle"}
        {disabled}
        onclick={toggle}
        class="
			relative block shrink-0 rounded-[3px] border
			{sizeConfig.track}
			shadow-[inset_0_1px_0_rgb(0_0_0/0.1)]
			focus:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2
			{checked ? 'border-primary-active bg-primary' : 'border-outline-hover bg-surface-sunken'}
			{disabled ? 'cursor-not-allowed' : 'cursor-pointer'}
		"
    >
        <span
            class="
				absolute top-px left-px flex items-center justify-center gap-0.5 rounded-[3px] border
				{checked ? 'border-primary-active' : 'border-outline-hover'} bg-surface-raised
				shadow-[inset_0_-1px_0_rgb(0_0_0/0.08),0_1px_0_rgb(0_0_0/0.06)]
				{sizeConfig.thumb}
				transform transition-transform duration-150 ease-out
				{checked ? sizeConfig.translate : 'translate-x-0'}
			"
        >
            <span class="w-px rounded-full {sizeConfig.grip} bg-outline"></span>
            <span class="w-px rounded-full {sizeConfig.grip} bg-outline"></span>
        </span>
    </button>

    {#if label || description}
        <div class="flex flex-col gap-0.5">
            {#if label}
                <span class="{sizeConfig.text} font-mono font-medium text-on-surface">{label}</span>
            {/if}
            {#if description}
                <span class="text-sm text-on-surface-muted">{description}</span>
            {/if}
        </div>
    {/if}
</label>
