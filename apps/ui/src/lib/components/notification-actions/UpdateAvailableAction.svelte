<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import Button from "@runwisp/ui/components/Button.svelte";
    import { systemStore } from "$lib/stores/system.svelte";
    import { systemApi } from "$lib/api";
    import { createLogger } from "$lib/utils/logger";
    import type { NotificationActionProps } from "./index";

    const logger = createLogger("UpdateAction");

    let { notification }: NotificationActionProps = $props();

    let updating = $state(false);
    let restarting = $state(false);
    let errorMsg = $state("");

    // Reset transient state once the daemon reports it is current again (it
    // restarted into the new version and the /api/daemon seed cleared the flag).
    $effect(() => {
        if (!systemStore.updateAvailable) {
            updating = false;
            restarting = false;
            errorMsg = "";
        }
    });

    // Only a self-updatable install still offering an update gets the in-place
    // button; docker/npm/manual installs show the upgrade command in the body.
    let canSelfUpdate = $derived(
        systemStore.updateAvailable && systemStore.updateMethod === "self",
    );

    async function update(): Promise<void> {
        updating = true;
        errorMsg = "";
        try {
            await systemApi.triggerUpdate();
            // The daemon swapped its binary and is re-execing; the connection
            // drops and comes back on the new version. State clears via the
            // effect above once /api/daemon reseeds as current.
            restarting = true;
        } catch (err) {
            errorMsg = err instanceof Error ? err.message : "Update failed";
            logger.warn("Self-update failed", err);
        } finally {
            updating = false;
        }
    }
</script>

{#if restarting}
    <p class="text-xs text-on-surface-muted" data-notification-id={notification.id}>
        Updating to <code class="font-semibold">{systemStore.latestVersion}</code> — the daemon is restarting.
    </p>
{:else if errorMsg}
    <div class="flex items-center gap-2" data-notification-id={notification.id}>
        <span class="text-danger-text text-xs">Update failed: {errorMsg}</span>
        <Button variant="primary" size="sm" loading={updating} onclick={update}>Retry</Button>
    </div>
{:else if canSelfUpdate}
    <div data-notification-id={notification.id}>
        <Button variant="primary" size="sm" loading={updating} onclick={update}>Update now</Button>
    </div>
{/if}
