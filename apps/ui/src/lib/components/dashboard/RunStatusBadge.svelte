<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import type { Snippet } from "svelte";

    // `tone` is a ready-made colour class pair, e.g. RUN_STATUS_CONFIG[status].badge.
    let {
        tone,
        class: className = "",
        children,
    }: { tone: string; class?: string; children: Snippet } = $props();
</script>

<span
    class="inline-flex rounded-[3px] px-1.5 py-0.5 font-mono text-2xs font-semibold {tone} {className}"
>
    {@render children()}
</span>
