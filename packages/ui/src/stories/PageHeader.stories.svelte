<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script module>
    import { defineMeta } from "@storybook/addon-svelte-csf";
    import PageHeader from "$lib/components/PageHeader.svelte";
    import Button from "$lib/components/Button.svelte";

    const { Story } = defineMeta({
        title: "Layout/PageHeader",
        component: PageHeader,
        tags: ["autodocs"],
    });
</script>

<Story
    name="Default"
    args={{ title: "Tasks", subtitle: "Scheduled tasks and background processes." }}
/>

<Story name="Rich subtitle and actions" asChild>
    <PageHeader title="Notifications">
        {#snippet subtitle()}
            Recent notifications across all tasks ·
            <span class="font-mono font-medium text-primary-soft-text tabular-nums">3 unread</span>
        {/snippet}
        {#snippet actions()}
            <Button size="sm" variant="secondary">Mark all read</Button>
        {/snippet}
    </PageHeader>
</Story>
