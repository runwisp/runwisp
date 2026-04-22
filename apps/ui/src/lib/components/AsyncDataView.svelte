<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts" generics="T">
    import type { Snippet } from "svelte";
    import { Skeleton, ErrorState } from "@runwisp/ui";
    import ConnectionLostPanel from "./ConnectionLostPanel.svelte";
    import { connectionStore } from "$lib/stores";
    import type { AsyncData } from "$lib/utils/async-data.svelte";

    let {
        data,
        skeletonRows = 4,
        children,
    }: {
        data: AsyncData<T>;
        skeletonRows?: number;
        children: Snippet;
    } = $props();
</script>

{#if data.loading && data.data === undefined}
    <Skeleton rows={skeletonRows} />
{:else if connectionStore.status !== "connected" && data.data === undefined}
    <ConnectionLostPanel />
{:else if data.error !== undefined && data.data === undefined}
    <ErrorState message={data.error} onRetry={data.reload} retrying={data.loading} />
{:else}
    {@render children()}
{/if}
