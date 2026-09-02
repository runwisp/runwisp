<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { CircleArrowUp, X } from "@lucide/svelte";
    import Button from "@runwisp/ui/components/Button.svelte";
    import { systemStore } from "$lib/stores/system.svelte";
    import { systemApi } from "$lib/api";
    import { createLogger } from "$lib/utils/logger";

    const logger = createLogger("UpdateBanner");

    let dismissed = $state(false);
    let updating = $state(false);
    let restarting = $state(false);
    let errorMsg = $state("");

    // Re-arm the banner once the update is no longer offered (daemon restarted
    // into the new version, so the check now reports current), so a later release
    // shows it again even after a dismissal.
    $effect(() => {
        if (!systemStore.updateAvailable) {
            dismissed = false;
            updating = false;
            restarting = false;
            errorMsg = "";
        }
    });

    let visible = $derived(systemStore.updateAvailable && !dismissed);
    let canSelfUpdate = $derived(systemStore.updateMethod === "self");

    function upgradeCommand(method: string): string {
        if (method === "docker") return "docker pull runwisp/runwisp";
        if (method === "npm") return "npm update -g runwisp";
        return "curl -fsSL https://get.runwisp.com | sh";
    }

    async function update() {
        updating = true;
        errorMsg = "";
        try {
            await systemApi.triggerUpdate();
            // The daemon swapped its binary and is re-execing; it will drop the
            // connection and come back on the new version. The connection layer
            // reconnects and the banner clears itself on the next /api/daemon seed.
            restarting = true;
        } catch (err) {
            errorMsg = err instanceof Error ? err.message : "Update failed";
            logger.warn("Self-update failed", err);
        } finally {
            updating = false;
        }
    }
</script>

{#if visible}
    <div
        role="status"
        class="flex items-center gap-3 border-b border-primary-soft-border bg-primary-soft px-6 py-2 text-sm text-primary-soft-text"
    >
        <CircleArrowUp size={16} class="shrink-0" />
        <span class="flex-1">
            {#if restarting}
                RunWisp is updating to <code class="font-semibold">{systemStore.latestVersion}</code
                >
                — the daemon is restarting.
            {:else if errorMsg}
                Update to <code class="font-semibold">{systemStore.latestVersion}</code> failed: {errorMsg}
            {:else}
                RunWisp <code class="font-semibold">{systemStore.latestVersion}</code> is available.
                {#if !canSelfUpdate}
                    Upgrade with <code class="font-semibold"
                        >{upgradeCommand(systemStore.updateMethod)}</code
                    >.
                {/if}
            {/if}
        </span>

        {#if canSelfUpdate && !restarting}
            <Button variant="primary" size="sm" loading={updating} onclick={update}>
                {errorMsg ? "Retry" : "Update now"}
            </Button>
        {/if}

        <button
            type="button"
            aria-label="Dismiss"
            class="rounded-[3px] p-1 hover:bg-primary-soft-border/50"
            onclick={() => (dismissed = true)}
        >
            <X size={16} />
        </button>
    </div>
{/if}
