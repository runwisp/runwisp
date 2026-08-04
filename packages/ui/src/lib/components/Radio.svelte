<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    type RadioSize = "sm" | "md" | "lg";

    interface Props {
        value: string;
        groupValue?: string;
        name: string;
        label: string;
        description?: string;
        disabled?: boolean;
        size?: RadioSize;
        class?: string;
    }

    let {
        value,
        groupValue = $bindable(""),
        name,
        label,
        description,
        disabled = false,
        size = "md",
        class: className = "",
    }: Props = $props();

    const sizeClasses: Record<RadioSize, { radio: string; dot: string; text: string }> = {
        sm: { radio: "h-4 w-4", dot: "h-1.5 w-1.5", text: "text-sm" },
        md: { radio: "h-5 w-5", dot: "h-2 w-2", text: "text-sm" },
        lg: { radio: "h-6 w-6", dot: "h-2.5 w-2.5", text: "text-base" },
    };

    const isChecked = $derived(groupValue === value);
</script>

<label
    class="inline-flex items-start gap-3 {disabled
        ? 'cursor-not-allowed opacity-50'
        : 'cursor-pointer'} {className}"
>
    <div class="relative flex shrink-0 items-center justify-center">
        <input
            type="radio"
            {name}
            {value}
            {disabled}
            checked={isChecked}
            onchange={() => (groupValue = value)}
            class="
                peer {sizeClasses[size].radio}
                cursor-pointer appearance-none rounded-full
                border border-outline
                bg-surface-raised checked:border-primary checked:bg-primary
                hover:border-outline-hover
                checked:hover:border-primary-hover checked:hover:bg-primary-hover
                focus:ring-2 focus:ring-ring focus:ring-offset-2
                focus:outline-none
                disabled:cursor-not-allowed disabled:hover:border-outline
                disabled:checked:hover:bg-primary
            "
        />
        <div
            class="pointer-events-none absolute rounded-full bg-on-primary opacity-0 peer-checked:opacity-100 {sizeClasses[
                size
            ].dot}"
        ></div>
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
