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

    const sizeClasses: Record<
        "sm" | "md" | "lg",
        { track: string; thumb: string; translate: string; text: string }
    > = {
        sm: { track: "h-5 w-9", thumb: "h-4 w-4", translate: "translate-x-4", text: "text-sm" },
        md: { track: "h-6 w-11", thumb: "h-5 w-5", translate: "translate-x-5", text: "text-sm" },
        lg: { track: "h-7 w-14", thumb: "h-6 w-6", translate: "translate-x-7", text: "text-base" },
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
			relative inline-flex shrink-0 items-center rounded-full
			{sizeConfig.track}
			transition-colors duration-200
			focus:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2
			{checked ? 'bg-primary' : 'bg-outline-hover'}
			{disabled ? 'cursor-not-allowed' : 'cursor-pointer'}
		"
    >
        <span
            class="
				inline-block rounded-full bg-surface-raised shadow-sm
				{sizeConfig.thumb}
				transform transition-transform duration-200
				{checked ? sizeConfig.translate : 'translate-x-0.5'}
			"
        ></span>
    </button>

    {#if label || description}
        <div class="flex flex-col gap-0.5">
            {#if label}
                <span class="{sizeConfig.text} font-medium text-on-surface">{label}</span>
            {/if}
            {#if description}
                <span class="text-sm text-on-surface-muted">{description}</span>
            {/if}
        </div>
    {/if}
</label>
