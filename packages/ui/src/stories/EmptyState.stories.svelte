<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script module>
    import { defineMeta } from "@storybook/addon-svelte-csf";
    import EmptyState from "$lib/components/EmptyState.svelte";
    import Button from "$lib/components/Button.svelte";
    import { Calendar, Search, TriangleAlert, Plus, RefreshCw } from "@lucide/svelte";

    const { Story } = defineMeta({
        title: "Core/EmptyState",
        component: EmptyState,
        tags: ["autodocs"],
    });
</script>

<Story name="Default" args={{ title: "No data", description: "There's nothing here yet." }} />

<Story name="No Tasks" asChild>
    <EmptyState
        icon={Calendar}
        title="No scheduled tasks"
        description="Create your first scheduled task to start automating tasks."
    >
        {#snippet actions()}
            <Button variant="primary">
                {#snippet icon()}<Plus size={16} />{/snippet}
                Create Task
            </Button>
        {/snippet}
    </EmptyState>
</Story>

<Story name="No Search Results" asChild>
    <EmptyState
        icon={Search}
        title="No results found"
        description="Try adjusting your search terms or filters to find what you're looking for."
    >
        {#snippet actions()}
            <Button variant="secondary">Clear Filters</Button>
        {/snippet}
    </EmptyState>
</Story>

<Story name="Error State" asChild>
    <EmptyState
        icon={TriangleAlert}
        title="Something went wrong"
        description="We couldn't load the data. Please try again or contact support if the problem persists."
    >
        {#snippet actions()}
            <Button variant="primary">
                {#snippet icon()}<RefreshCw size={16} />{/snippet}
                Try Again
            </Button>
            <Button variant="ghost">Contact Support</Button>
        {/snippet}
    </EmptyState>
</Story>

<Story name="In Card Context" asChild>
    <div class="max-w-lg rounded-xl border border-outline bg-white p-6">
        <EmptyState
            icon={Calendar}
            title="No upcoming executions"
            description="All scheduled tasks are either paused or have completed."
        >
            {#snippet actions()}
                <Button variant="secondary" size="sm">View All Tasks</Button>
            {/snippet}
        </EmptyState>
    </div>
</Story>
