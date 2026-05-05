<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { sparkline } from "$lib/utils/notification-rhythm";

    interface Props {
        occurrences: string[];
        now: Date;
        windowMs?: number;
        cells?: number;
        class?: string;
    }

    let {
        occurrences,
        now,
        windowMs = 60 * 60 * 1000,
        cells = 8,
        class: className = "",
    }: Props = $props();

    let rendered = $derived(sparkline(occurrences, now, windowMs, cells));
</script>

{#if rendered}
    <span
        aria-label="Recent occurrences sparkline"
        class="font-mono tracking-tight tabular-nums {className}">{rendered}</span
    >
{/if}
