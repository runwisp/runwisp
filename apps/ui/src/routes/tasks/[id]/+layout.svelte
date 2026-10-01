<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import type { Snippet } from "svelte";
    import { page } from "$app/stores";

    let { children }: { children: Snippet } = $props();
</script>

<!-- Remount the task page when the task changes (sidebar click) so its runs,
     definition and per-task state start fresh behind the first-load skeletons,
     instead of showing the previous task's data or an empty "No runs yet". Keyed
     on the task only: selecting a run (the [[runId]] segment) keeps the page. -->
{#key $page.params.id}
    {@render children()}
{/key}
