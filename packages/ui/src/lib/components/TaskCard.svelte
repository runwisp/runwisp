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

    // Each accent maps to a left keyline in the matching semantic colour. The
    // other three sides stay a neutral hairline (set per-side so the shorthand
    // never overrides the accent left-colour), and hover lifts the surface.
    const accentClasses: Record<TaskCardAccent, string> = {
        danger: "border-l-danger-surface",
        wisp: "border-l-primary",
        aurora: "border-l-info",
    };
</script>

<button
    type="button"
    class="group w-full rounded-[4px] border border-l-2 border-t-outline border-r-outline border-b-outline bg-surface-raised p-3 text-left hover:bg-surface-sunken {accentClasses[
        accent
    ]} {className}"
    {onclick}
>
    {@render children()}
</button>
