<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";

    export type TaskCardAccent = "danger" | "wisp" | "aurora";

    interface Props {
        accent?: TaskCardAccent;
        onclick?: () => void;
        children: Snippet;
        class?: string;
    }

    let { accent = "wisp", onclick, children, class: className = "" }: Props = $props();

    const accentClasses: Record<TaskCardAccent, string> = {
        danger: "hover:border-danger-soft-border",
        wisp: "hover:border-primary-soft-border",
        aurora: "hover:border-info-soft-border",
    };
</script>

<button
    type="button"
    class="group w-full rounded-lg border border-outline-faint bg-surface-raised p-3 text-left transition-all hover:shadow-sm {accentClasses[
        accent
    ]} {className}"
    {onclick}
>
    {@render children()}
</button>
