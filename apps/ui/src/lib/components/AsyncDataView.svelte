<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts" generics="T">
    import type { Snippet } from "svelte";
    import { Skeleton, ErrorState } from "@runwisp/ui";
    import ConnectionLostPanel from "./ConnectionLostPanel.svelte";
    import { connectionStore } from "$lib/stores";
    import type { AsyncData } from "$lib/utils/async-data.svelte";

    let {
        data,
        skeleton,
        children,
    }: {
        data: AsyncData<T>;
        /** Page-shaped placeholder for the first load; generic rows otherwise. */
        skeleton?: Snippet;
        children: Snippet;
    } = $props();
</script>

<!-- Once data has loaded, keep showing it through refetches, errors and drops. -->
{#if typeof data.data !== "undefined"}
    {@render children()}
{:else if data.loading}
    {#if skeleton}
        {@render skeleton()}
    {:else}
        <Skeleton rows={4} />
    {/if}
{:else if connectionStore.status !== "connected"}
    <ConnectionLostPanel />
{:else if typeof data.error !== "undefined"}
    <ErrorState message={data.error} onRetry={data.fetch} retrying={data.loading} />
{:else}
    {@render children()}
{/if}
