<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script module>
    import { defineMeta } from "@storybook/addon-svelte-csf";
    import Table from "$lib/components/Table.svelte";
    import TableBody from "$lib/components/TableBody.svelte";
    import TableCell from "$lib/components/TableCell.svelte";
    import TableHead from "$lib/components/TableHead.svelte";
    import TableRow from "$lib/components/TableRow.svelte";

    const { Story } = defineMeta({
        title: "Data/Table",
        component: Table,
        tags: ["autodocs"],
    });

    const runs = [
        { id: "01J9Z3", task: "backup-db", status: "success", duration: "12s" },
        { id: "01J9Z4", task: "sync-files", status: "failed", duration: "3m 1s" },
        { id: "01J9Z5", task: "cleanup", status: "success", duration: "800ms" },
    ];
</script>

<Story name="Default" asChild>
    <Table class="max-w-xl">
        <TableHead>
            <TableCell header>Task</TableCell>
            <TableCell header aria-sort="descending">Status</TableCell>
            <TableCell header align="right">Duration</TableCell>
        </TableHead>
        <TableBody>
            {#each runs as run (run.id)}
                <TableRow border="faint" hoverable>
                    <TableCell class="font-mono">{run.task}</TableCell>
                    <TableCell class="text-on-surface-muted">{run.status}</TableCell>
                    <TableCell align="right" class="font-mono">{run.duration}</TableCell>
                </TableRow>
            {/each}
        </TableBody>
    </Table>
</Story>

<Story name="Compact, two-row header" asChild>
    <Table class="max-w-xl">
        <TableHead row={false}>
            <TableRow border="faint">
                <TableCell header density="compact" colspan={2}>Run</TableCell>
                <TableCell header density="compact" align="right">Timing</TableCell>
            </TableRow>
            <TableRow>
                <TableCell header density="compact">Id</TableCell>
                <TableCell header density="compact">Task</TableCell>
                <TableCell header density="compact" align="right">Duration</TableCell>
            </TableRow>
        </TableHead>
        <TableBody>
            {#each runs as run (run.id)}
                <TableRow border="faint">
                    <TableCell density="compact" class="font-mono">{run.id}</TableCell>
                    <TableCell density="compact" class="font-mono">{run.task}</TableCell>
                    <TableCell density="compact" align="right" class="font-mono"
                        >{run.duration}</TableCell
                    >
                </TableRow>
            {/each}
        </TableBody>
    </Table>
</Story>
