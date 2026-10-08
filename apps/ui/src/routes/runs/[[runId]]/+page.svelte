<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { page } from "$app/stores";
    import { resolve } from "$app/paths";
    import { RunsPage } from "$lib/components/dashboard";
    import { createLiveRuns } from "$lib/utils/live-runs.svelte";
    import { navigateToRun } from "$lib/utils/run-url";
    import { emptyRunFilters, type RunsListFilters } from "@runwisp/ui";

    const live = createLiveRuns();

    // The selected run lives in the path as an optional segment: /runs/{runId}.
    let runIdParam = $derived($page.params.runId ?? null);

    // Mirror the user-selected run into the address bar so the URL is shareable;
    // null drops back to /runs.
    function selectRun(runId: string | null) {
        navigateToRun($page.url, runId ? resolve(`/runs/${runId}`) : resolve("/runs"));
    }

    let filters = $state<RunsListFilters>(emptyRunFilters());

    $effect(() => {
        live.source.setFilters({ ...filters });
    });

    $effect(() => live.deepLink.resolve(runIdParam, live.source.items));
</script>

<RunsPage {live} bind:filters initialRunId={runIdParam} onSelectRun={selectRun} />
