<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { page } from "$app/state";
    import { ErrorState } from "@runwisp/ui";
    import ConnectionLostPanel from "$lib/components/ConnectionLostPanel.svelte";
    import { connectionStore } from "$lib/stores";

    // If the daemon is unreachable the likely cause is a failed dynamic import
    // on navigation — show the polished disconnect UX rather than a raw 500.
    let showConnectionLost = $derived(connectionStore.status !== "connected");

    // Once the daemon is back, reload so SvelteKit can fetch the missing route
    // chunk and mount the requested page.
    $effect(() => connectionStore.onReconnect(() => location.reload()));
</script>

{#if showConnectionLost}
    <ConnectionLostPanel />
{:else}
    <ErrorState
        title={page.status.toString()}
        message={page.error?.message ?? "Something went wrong."}
        onRetry={() => location.reload()}
    />
{/if}
