<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { setContext, type Snippet } from "svelte";
    import { RADIO_GROUP_KEY, type RadioGroupContext } from "./radio-group.js";

    type Orientation = "horizontal" | "vertical";

    interface Props {
        value?: string;
        name: string;
        label?: string;
        error?: string;
        hint?: string;
        orientation?: Orientation;
        children: Snippet;
        class?: string;
    }

    let {
        value = $bindable(""),
        name,
        label,
        error,
        hint,
        orientation = "vertical",
        children,
        class: className = "",
    }: Props = $props();

    setContext<RadioGroupContext>(RADIO_GROUP_KEY, {
        get name() {
            return name;
        },
        get value() {
            return value;
        },
        set value(next: string) {
            value = next;
        },
    });

    const orientationClasses: Record<Orientation, string> = {
        horizontal: "flex flex-row flex-wrap gap-x-6 gap-y-3",
        vertical: "flex flex-col gap-3",
    };
</script>

<fieldset class="space-y-3 {className}" role="radiogroup" aria-label={label}>
    {#if label}
        <legend class="font-mono text-xs font-medium text-on-surface-muted">{label}</legend>
    {/if}

    <div class={orientationClasses[orientation]}>
        {@render children()}
    </div>

    {#if error}
        <p class="font-sans text-xs text-danger-soft-text">
            {error}
        </p>
    {:else if hint}
        <p class="font-sans text-xs text-on-surface-muted">{hint}</p>
    {/if}
</fieldset>
