<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { ShieldOff } from "@lucide/svelte";
    import { authStore } from "$lib/stores/auth.svelte";

    // Persistent (no dismiss) on purpose: a daemon running with
    // RUNWISP_NO_AUTH must never be mistaken for a secured instance.
    let visible = $derived(authStore.current.loaded && !authStore.current.required);
</script>

{#if visible}
    <span
        class="flex shrink-0 items-center gap-1.5 rounded-full border border-warning-soft-border bg-warning-soft px-2.5 py-1 text-xs font-medium whitespace-nowrap text-warning-soft-text"
        title="RUNWISP_NO_AUTH is set — the API and Web UI are reachable without a password. Local/dev use only."
    >
        <ShieldOff size={12} class="shrink-0" />
        <span class="hidden sm:inline">Auth disabled</span>
    </span>
{/if}
