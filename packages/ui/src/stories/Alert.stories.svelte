<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script module>
    import { defineMeta } from "@storybook/addon-svelte-csf";
    import Alert from "$lib/components/Alert.svelte";
    import Button from "$lib/components/Button.svelte";

    const { Story } = defineMeta({
        title: "Core/Alert",
        component: Alert,
        tags: ["autodocs"],
        argTypes: {
            variant: {
                control: "select",
                options: ["info", "success", "warning", "danger"],
            },
        },
    });
</script>

<Story name="Info" args={{ variant: "info", title: "Scheduled maintenance" }}>
    The system will undergo maintenance on January 20th from 2:00 AM to 4:00 AM UTC.
</Story>

<Story name="Success" args={{ variant: "success", title: "Task completed successfully" }}>
    The backup task has finished processing 1,234 records.
</Story>

<Story name="Warning" args={{ variant: "warning", title: "Approaching rate limit" }}>
    You have used 80% of your monthly task executions.
</Story>

<Story name="Danger" args={{ variant: "danger", title: "Task execution failed" }}>
    The sync-data task failed after 3 retry attempts. Check logs for details.
</Story>

<Story
    name="Dismissible"
    args={{ variant: "info", title: "New feature available", dismissible: true }}
>
    You can now schedule tasks using natural language expressions.
</Story>

<Story name="With Actions" asChild>
    <Alert variant="danger" title="Task failed">
        The scheduled task "backup-database" has failed.
        {#snippet actions()}
            <div class="flex gap-2">
                <Button variant="danger" size="sm">View Logs</Button>
                <Button variant="ghost" size="sm">Retry Task</Button>
            </div>
        {/snippet}
    </Alert>
</Story>

<Story name="All Variants" asChild>
    <div class="max-w-lg space-y-4">
        <Alert variant="info" title="Information">This is an informational message.</Alert>
        <Alert variant="success" title="Success">Operation completed successfully.</Alert>
        <Alert variant="warning" title="Warning">Please review before proceeding.</Alert>
        <Alert variant="danger" title="Error">An error has occurred.</Alert>
    </div>
</Story>
