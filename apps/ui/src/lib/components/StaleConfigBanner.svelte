<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { TriangleAlert, X } from "@lucide/svelte";
    import { systemStore } from "$lib/stores/system.svelte";

    let dismissed = $state(false);

    // Re-arm the banner once the daemon restarts (staleness clears), so the
    // next config edit shows it again even after a dismissal.
    $effect(() => {
        if (!systemStore.configStale) dismissed = false;
    });

    let visible = $derived(systemStore.configStale && !dismissed);
</script>

{#if visible}
    <div
        role="status"
        class="flex items-center gap-3 border-b border-warning-soft-border bg-warning-soft px-6 py-2 text-sm text-warning-soft-text"
    >
        <TriangleAlert size={16} class="shrink-0" />
        <span class="flex-1">
            <code class="font-semibold">runwisp.toml</code> has changed since the daemon started —
            run <code class="font-semibold">runwisp reload</code> to apply. The UI never edits config;
            your file is the source of truth.
        </span>
        <button
            type="button"
            aria-label="Dismiss"
            class="rounded-[3px] p-1 hover:bg-warning-soft-border/50"
            onclick={() => (dismissed = true)}
        >
            <X size={16} />
        </button>
    </div>
{/if}
