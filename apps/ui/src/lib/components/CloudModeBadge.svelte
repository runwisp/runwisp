<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { Cloud } from "@lucide/svelte";
    import { systemStore } from "$lib/stores/system.svelte";

    // One tasteful, persistent chip — not a banner. It signals that this
    // runner's page is part of RunWisp Cloud, where scheduling and dispatch
    // live; the local UI only shows what runs on this machine.
    let visible = $derived(systemStore.cloudEnabled);
</script>

{#if visible}
    <span
        class="flex items-center gap-1.5 rounded-full border border-info-soft-border bg-info-soft px-2.5 py-1 text-xs font-medium text-info-soft-text"
        title="This runner is managed by RunWisp Cloud — scheduling and dispatch happen in the cloud; this page shows what runs on this machine."
    >
        <Cloud size={12} class="shrink-0" />
        RunWisp Cloud
    </span>
{/if}
