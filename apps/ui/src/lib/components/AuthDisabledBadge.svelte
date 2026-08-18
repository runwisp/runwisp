<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { ShieldOff } from "@lucide/svelte";
    import { Badge } from "@runwisp/ui";
    import { authStore } from "$lib/stores/auth.svelte";

    // Persistent (no dismiss) on purpose: a daemon running with
    // RUNWISP_AUTH=off must never be mistaken for a secured instance.
    let visible = $derived(authStore.current.loaded && !authStore.current.required);
</script>

{#if visible}
    <span
        title="RUNWISP_AUTH=off is set — the API and Web UI are reachable without a password. Local/dev use only."
    >
        <Badge variant="warning" class="shrink-0">
            <ShieldOff size={12} class="shrink-0" />
            <span class="hidden sm:inline">Auth disabled</span>
        </Badge>
    </span>
{/if}
