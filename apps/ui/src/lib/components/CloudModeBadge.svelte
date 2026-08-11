<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { Cloud } from "@lucide/svelte";
    import { Badge } from "@runwisp/ui";
    import { systemStore } from "$lib/stores/system.svelte";

    // One tasteful, persistent chip — not a banner. It signals that this
    // runner's page is part of RunWisp Cloud, where scheduling and dispatch
    // live; the local UI only shows what runs on this machine.
    let visible = $derived(systemStore.cloudEnabled);
</script>

{#if visible}
    <span
        title="This runner is managed by RunWisp Cloud — scheduling and dispatch happen in the cloud; this page shows what runs on this machine."
    >
        <Badge variant="info" class="shrink-0">
            <Cloud size={12} class="shrink-0" />
            <span class="hidden sm:inline">RunWisp Cloud</span>
        </Badge>
    </span>
{/if}
