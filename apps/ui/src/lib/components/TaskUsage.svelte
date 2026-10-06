<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { formatBytes, Tooltip } from "@runwisp/ui";
    import { systemStore } from "$lib/stores/system.svelte";
    import type { Task } from "$lib/types";

    // Live CPU and memory of the task's running shell runs. Renders nothing
    // while nothing is running or the backend can't be measured.
    let { task }: { task: Task } = $props();

    const usage = $derived(systemStore.usageFor(task));
</script>

{#if usage}
    <Tooltip content="Live CPU (100% = one core) and memory of the running processes">
        <span
            class="font-mono text-2xs text-on-surface-muted tabular-nums"
            data-testid="task-usage"
        >
            {Math.round(usage.cpuPercent)}% · {formatBytes(usage.memoryBytes)}
        </span>
    </Tooltip>
{/if}
