<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { CircleAlert, Clock } from "@lucide/svelte";
    import ms, { type StringValue } from "ms";

    type InputSize = "sm" | "md" | "lg";

    interface Props {
        id?: string;
        value?: number | null;
        label?: string;
        hint?: string;
        error?: string;
        disabled?: boolean;
        class?: string;
        placeholder?: string;
        size?: InputSize;
    }

    let {
        id,
        value = $bindable(),
        label,
        hint,
        error = $bindable(),
        disabled = false,
        class: className = "",
        placeholder = "e.g. 5m 30s",
        size = "md",
        ...restProps
    }: Props = $props();

    const generatedId = $props.id();
    const inputId = $derived(id ?? generatedId);

    let textValue = $state("");
    let parsedHuman = $state("");
    let isFocused = $state(false);

    const sizeClasses: Record<InputSize, string> = {
        sm: "text-sm px-3 py-1.5",
        md: "text-sm px-3.5 py-2",
        lg: "text-base px-4 py-2.5",
    };

    const inputClasses = `
        w-full rounded-[3px] border font-mono
        bg-surface-raised text-on-surface placeholder:text-on-surface-faint
        focus:outline-none focus:border-ring focus:ring-2 focus:ring-ring focus:ring-offset-2
        disabled:bg-surface-sunken disabled:text-on-surface-muted disabled:cursor-not-allowed
            `;

    const normalBorder = "border-outline hover:border-outline-hover shadow-sm";
    const errorBorder =
        "border-danger-surface focus:border-danger-surface focus:ring-danger-surface";

    function formatMs(msValue: number): string {
        if (typeof msValue !== "number" || isNaN(msValue)) return "";
        try {
            return ms(msValue);
        } catch {
            return "";
        }
    }

    function parseToMs(str: string): number | null {
        if (!str || !str.trim()) return null;
        try {
            const result = ms(str as StringValue);
            return typeof result === "number" ? result : null;
        } catch {
            return null;
        }
    }

    function updateFromValue() {
        if (!isFocused) {
            textValue = value != null ? formatMs(value) : "";
        }
    }

    $effect(() => {
        updateFromValue();
    });

    function handleInput(e: Event & { currentTarget: EventTarget & HTMLInputElement }) {
        const val = e.currentTarget.value;
        textValue = val;

        const msValue = parseToMs(val);
        value = msValue;

        if (typeof msValue === "number") {
            parsedHuman = ms(msValue, { long: true });
            if (error) error = undefined;
        } else {
            parsedHuman = "";
        }
    }

    function handleFocus() {
        isFocused = true;
    }

    function handleBlur() {
        isFocused = false;
        textValue = value != null ? formatMs(value) : "";
        parsedHuman = "";
    }
</script>

<div class="space-y-1.5 {className}">
    {#if label}
        <label for={inputId} class="block font-mono text-xs font-medium text-on-surface-muted">
            {label}
        </label>
    {/if}

    <div class="group relative">
        <div
            class="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-on-surface-faint group-focus-within:text-primary"
        >
            <Clock size={16} />
        </div>

        <input
            id={inputId}
            type="text"
            class="
                {inputClasses}
                {sizeClasses[size]}
                {error ? errorBorder : normalBorder}
                pl-10
                {error ? 'pr-10' : ''}
            "
            {disabled}
            value={textValue}
            {placeholder}
            oninput={handleInput}
            onfocus={handleFocus}
            onblur={handleBlur}
            aria-invalid={error ? "true" : undefined}
            {...restProps}
        />

        {#if error}
            <div
                class="pointer-events-none absolute top-1/2 right-3 -translate-y-1/2 text-danger-soft-text"
            >
                <CircleAlert size={18} />
            </div>
        {/if}
    </div>

    {#if parsedHuman && isFocused}
        <p class="font-sans text-xs font-medium text-primary">
            {parsedHuman}
        </p>
    {:else if error}
        <p class="font-sans text-xs text-danger-soft-text">
            {error}
        </p>
    {:else if hint}
        <p class="font-sans text-xs text-on-surface-muted">{hint}</p>
    {/if}
</div>
