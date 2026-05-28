<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script module>
    import { defineMeta } from "@storybook/addon-svelte-csf";
    import DataGrid from "$lib/components/DataGrid.svelte";
    import StatusIndicator from "$lib/components/StatusIndicator.svelte";
    import Dropdown from "$lib/components/Dropdown.svelte";
    import { Play, Pause, Trash2, SquarePen } from "@lucide/svelte";

    const { Story } = defineMeta({
        title: "Data/DataGrid",
        component: DataGrid,
        tags: ["autodocs"],
    });

    const columns = [
        { key: "name", label: "Task Name", sortable: true },
        { key: "schedule", label: "Schedule" },
        { key: "lastRun", label: "Last Run", sortable: true },
        { key: "status", label: "Status" },
        { key: "duration", label: "Duration", align: /** @type {'right'} */ ("right") },
    ];

    const tasks = [
        {
            id: "1",
            name: "backup-database",
            schedule: "0 2 * * *",
            lastRun: "2024-01-15 02:00",
            status: "success",
            duration: "45s",
        },
        {
            id: "2",
            name: "cleanup-logs",
            schedule: "*/30 * * * *",
            lastRun: "2024-01-15 10:30",
            status: "running",
            duration: "12s",
        },
        {
            id: "3",
            name: "sync-data",
            schedule: "0 */4 * * *",
            lastRun: "2024-01-15 08:00",
            status: "failed",
            duration: "2m 15s",
        },
        {
            id: "4",
            name: "generate-report",
            schedule: "0 9 * * 1",
            lastRun: "2024-01-08 09:00",
            status: "success",
            duration: "1m 30s",
        },
        {
            id: "5",
            name: "send-notifications",
            schedule: "0 8 * * *",
            lastRun: "2024-01-15 08:00",
            status: "success",
            duration: "8s",
        },
    ];

    const menuItems = [
        { label: "Edit", icon: SquarePen },
        { label: "Run Now", icon: Play },
        { label: "Pause", icon: Pause },
        { divider: true },
        { label: "Delete", icon: Trash2, danger: true },
    ];
</script>

{#snippet statusCell(/** @type {any} */ row)}
    <div class="inline-flex items-center">
        <StatusIndicator status={row.status} size="sm" />
    </div>
{/snippet}

{#snippet durationCell(/** @type {any} */ row)}
    <span class="font-mono text-on-surface-muted">{row.duration}</span>
{/snippet}

<Story name="Basic" args={{ columns, data: tasks }} />

<Story name="With Custom Rendering" asChild>
    <DataGrid
        columns={[
            { key: "name", label: "Task Name", sortable: true },
            { key: "schedule", label: "Schedule" },
            { key: "lastRun", label: "Last Run", sortable: true },
            { key: "status", label: "Status", render: statusCell },
            {
                key: "duration",
                label: "Duration",
                align: /** @type {'right'} */ ("right"),
                render: durationCell,
            },
        ]}
        data={tasks}
    />
</Story>

<Story name="Selectable" args={{ columns, data: tasks, selectable: true }} />

<Story name="Sortable" args={{ columns, data: tasks, sortKey: "name", sortDirection: "asc" }} />

<Story
    name="Filterable"
    args={{ columns, data: tasks, filterable: true, filterPlaceholder: "Filter tasks…" }}
/>

<Story name="Paginated" args={{ columns, data: tasks, paginate: true, pageSize: 2 }} />

<Story name="Striped" args={{ columns, data: tasks, striped: true }} />

<Story name="Compact" args={{ columns, data: tasks, compact: true }} />

<Story name="Empty State" args={{ columns, data: [], emptyMessage: "No scheduled tasks found" }} />

<Story name="With Row Actions" asChild>
    <DataGrid {columns} data={tasks}>
        {#snippet rowAction()}
            <Dropdown items={menuItems} />
        {/snippet}
    </DataGrid>
</Story>
