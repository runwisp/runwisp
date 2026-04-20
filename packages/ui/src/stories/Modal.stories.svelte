<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script module>
    import { defineMeta } from "@storybook/addon-svelte-csf";
    import Modal from "$lib/components/Modal.svelte";
    import Button from "$lib/components/Button.svelte";
    import Input from "$lib/components/Input.svelte";
    import Select from "$lib/components/Select.svelte";

    const { Story } = defineMeta({
        title: "Core/Modal",
        component: Modal,
        tags: ["autodocs"],
        argTypes: {
            size: {
                control: "select",
                options: ["sm", "md", "lg", "xl", "full"],
            },
        },
    });

    let basicOpen = $state(false);
    let formOpen = $state(false);
    let confirmOpen = $state(false);
    let largeOpen = $state(false);

    const scheduleTypes = [
        { value: "cron", label: "Cron Expression" },
        { value: "interval", label: "Fixed Interval" },
    ];
</script>

<Story name="Basic" asChild>
    <Button onclick={() => (basicOpen = true)}>Open Modal</Button>
    <Modal
        bind:open={basicOpen}
        title="Modal Title"
        description="This is a description of the modal."
    >
        <p class="text-mist-600">This is the modal content. You can put any content here.</p>
    </Modal>
</Story>

<Story name="With Form" asChild>
    <Button onclick={() => (formOpen = true)}>Create New Task</Button>
    <Modal bind:open={formOpen} title="Create New Task" size="lg">
        <div class="space-y-4">
            <Input label="Task Name" placeholder="my-scheduled-task" />
            <Select label="Schedule Type" options={scheduleTypes} placeholder="Select type..." />
            <Input label="Cron Expression" placeholder="*/5 * * * *" hint="Standard cron syntax" />
        </div>
        {#snippet footer()}
            <div class="flex justify-end gap-3">
                <Button variant="ghost" onclick={() => (formOpen = false)}>Cancel</Button>
                <Button variant="primary">Create Task</Button>
            </div>
        {/snippet}
    </Modal>
</Story>

<Story name="Confirmation Dialog" asChild>
    <Button variant="danger" onclick={() => (confirmOpen = true)}>Delete Task</Button>
    <Modal bind:open={confirmOpen} title="Delete Task?" size="sm">
        <p class="text-mist-600">
            Are you sure you want to delete <strong>backup-database</strong>? This action cannot be
            undone.
        </p>
        {#snippet footer()}
            <div class="flex justify-end gap-3">
                <Button variant="ghost" onclick={() => (confirmOpen = false)}>Cancel</Button>
                <Button variant="danger" onclick={() => (confirmOpen = false)}>Delete</Button>
            </div>
        {/snippet}
    </Modal>
</Story>

<Story name="Large Modal" asChild>
    <Button onclick={() => (largeOpen = true)}>View Task Logs</Button>
    <Modal bind:open={largeOpen} title="Task Execution Logs" size="full">
        <div
            class="max-h-96 overflow-auto rounded-lg bg-mist-900 p-4 font-mono text-sm text-mist-100"
        >
            <p class="text-aurora-400">Starting task execution...</p>
            <p class="text-mist-300">Connecting to database...</p>
            <p class="text-mist-300">Connection established</p>
            <p class="text-mist-300">Running backup query...</p>
            <p class="text-mist-300">Processing 1,234 records...</p>
            <p class="text-mist-300">Compressing backup file...</p>
            <p class="text-mist-300">Uploading to storage...</p>
            <p class="text-success-400">Task completed successfully</p>
            <p class="text-mist-500">Total execution time: 90s</p>
        </div>
        {#snippet footer()}
            <div class="flex justify-between">
                <Button variant="ghost">Download Logs</Button>
                <Button variant="primary" onclick={() => (largeOpen = false)}>Close</Button>
            </div>
        {/snippet}
    </Modal>
</Story>
