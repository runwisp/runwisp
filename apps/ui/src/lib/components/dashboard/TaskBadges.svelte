<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import type { Task } from "@runwisp/common";
    import { Badge } from "@runwisp/ui";
    import ComposeBadge from "../ComposeBadge.svelte";
    import TaskHeldBadge from "../TaskHeldBadge.svelte";
    import TaskSourceBadge from "../TaskSourceBadge.svelte";

    let { task }: { task: Task } = $props();
</script>

{#if task.group}
    <Badge variant="default" size="sm">{task.group}</Badge>
{/if}
{#if task.compose}
    <ComposeBadge
        file={task.compose.file}
        service={task.compose.service}
        projectName={task.compose.projectName}
    />
{/if}
{#if task.heldBy}
    <TaskHeldBadge />
{/if}
{#if task.source}
    <TaskSourceBadge name={task.name} source={task.source} sourceFile={task.sourceFile} />
{/if}
