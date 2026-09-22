<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { RadioTower } from "@lucide/svelte";
    import { Badge } from "@runwisp/ui";
    import { systemStore } from "$lib/stores/system.svelte";

    // One tasteful, persistent chip — not a banner. It signals that this
    // runner's page is part of RunWisp Station, where scheduling and dispatch
    // live; the local UI only shows what runs on this machine.
    let visible = $derived(systemStore.stationEnabled);
</script>

{#if visible}
    <Badge
        variant="info"
        class="shrink-0"
        tooltip="This runner is managed by RunWisp Station — scheduling and dispatch happen in the station; this page shows what runs on this machine."
        tooltipPosition="bottom"
    >
        <RadioTower size={12} class="shrink-0" />
        <span class="hidden sm:inline">RunWisp Station</span>
    </Badge>
{/if}
