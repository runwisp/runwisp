<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script module>
    import { defineMeta } from "@storybook/addon-svelte-csf";
    import Breadcrumb from "$lib/components/Breadcrumb.svelte";
    import { House } from "@lucide/svelte";

    const { Story } = defineMeta({
        title: "Navigation/Breadcrumb",
        component: Breadcrumb,
        tags: ["autodocs"],
    });
</script>

<Story
    name="Default"
    args={{
        items: [
            { label: "Dashboard", href: "/" },
            { label: "Tasks", href: "/tasks" },
            { label: "backup-database" },
        ],
    }}
/>

<Story
    name="With Home Icon"
    args={{
        items: [
            { label: "Home", href: "/", icon: House },
            { label: "Schedules", href: "/schedules" },
            { label: "Create New" },
        ],
    }}
/>

<Story
    name="Long Path"
    args={{
        items: [
            { label: "Home", href: "/" },
            { label: "Projects", href: "/projects" },
            { label: "RunWisp", href: "/projects/runwisp" },
            { label: "Tasks", href: "/projects/runwisp/tasks" },
            { label: "backup-database" },
        ],
    }}
/>

<Story name="In Page Context" asChild>
    <div class="rounded-xl border border-outline bg-white p-6">
        <Breadcrumb
            items={[
                { label: "Dashboard", href: "/" },
                { label: "Tasks", href: "/tasks" },
                { label: "backup-database" },
            ]}
            class="mb-4"
        />
        <h1 class="text-2xl font-semibold text-on-surface">backup-database</h1>
        <p class="mt-1 text-on-surface-muted">Daily database backup task</p>
    </div>
</Story>
