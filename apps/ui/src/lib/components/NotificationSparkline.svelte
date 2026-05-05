<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { sparkline } from "$lib/utils/notification-rhythm";
    import { clockStore } from "$lib/stores";

    interface Props {
        occurrences: string[];
        windowMs?: number;
        cells?: number;
        class?: string;
    }

    let {
        occurrences,
        windowMs = 60 * 60 * 1000,
        cells = 8,
        class: className = "",
    }: Props = $props();

    let rendered = $derived(sparkline(occurrences, new Date(clockStore.now), windowMs, cells));
</script>

{#if rendered}
    <span
        aria-label="Recent occurrences sparkline"
        class="font-mono tracking-tight tabular-nums {className}">{rendered}</span
    >
{/if}
