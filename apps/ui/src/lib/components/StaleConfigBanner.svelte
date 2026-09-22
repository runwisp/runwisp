<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { TriangleAlert, X } from "@lucide/svelte";
    import { toast, extractErrorMessage } from "@runwisp/ui";
    import { systemStore } from "$lib/stores/system.svelte";
    import { systemApi } from "$lib/api";
    import { reloadSummary } from "./stale-config-banner.js";

    let dismissed = $state(false);
    let reloading = $state(false);

    // Re-arm the banner once the daemon restarts (staleness clears), so the
    // next config edit shows it again even after a dismissal.
    $effect(() => {
        if (!systemStore.configStale) dismissed = false;
    });

    let visible = $derived(systemStore.configStale && !dismissed);

    // The banner itself clears reactively once the daemon's next config.stale
    // SSE tick reports fresh state (see system.svelte.ts) — no extra refetch here.
    async function handleReload() {
        reloading = true;
        try {
            const result = await systemApi.reload();
            toast.success(reloadSummary(result));
        } catch (err) {
            toast.error(extractErrorMessage(err, "Failed to reload config"));
        } finally {
            reloading = false;
        }
    }
</script>

{#if visible}
    <div
        role="status"
        class="flex items-center gap-3 border-b border-warning-soft-border bg-warning-soft px-6 py-2 text-sm text-warning-soft-text"
    >
        <TriangleAlert size={16} class="shrink-0" />
        <span class="flex-1">
            <code class="font-semibold">runwisp.toml</code> has changed since the daemon started. The
            UI never edits config; your file is the source of truth.
        </span>
        <button
            type="button"
            disabled={reloading}
            class="rounded-[3px] border border-warning-soft-border px-2 py-1 font-semibold hover:bg-warning-soft-border/50 disabled:opacity-50"
            onclick={handleReload}
        >
            {reloading ? "Reloading…" : "Reload"}
        </button>
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
