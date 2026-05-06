<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { Activity, Box, History } from "@lucide/svelte";
    import Logo from "@runwisp/ui/components/Logo.svelte";
    import { type Snippet, type Component } from "svelte";
    import { resolve } from "$app/paths";
    import ConnectionStatusIndicator from "$lib/components/ConnectionStatusIndicator.svelte";
    import NotificationBell from "$lib/components/NotificationBell.svelte";

    let {
        activePage,
        tasks = [],
        urls = {
            overview: "#",
            runs: "#",
        },
        children,
    } = $props<{
        activePage: string;
        tasks?: { id: string; name: string; group?: string; icon?: Component; href?: string }[];
        urls?: { overview: string; runs: string };
        children: Snippet;
    }>();

    type TaskGroup = { name: string; tasks: typeof tasks };

    let taskGroups: TaskGroup[] = $derived.by(() => {
        const groups: Record<string, typeof tasks> = {};
        for (const task of tasks) {
            const groupName = task.group ?? "Tasks";
            let groupTasks = groups[groupName];
            if (!groupTasks) {
                groupTasks = [];
                groups[groupName] = groupTasks;
            }
            groupTasks.push(task);
        }
        return Object.entries(groups).map(([name, groupTasks]) => ({ name, tasks: groupTasks }));
    });

    let showGroupHeaders = $derived(taskGroups.length > 1);
</script>

<div
    class="flex h-screen w-full bg-mist-50 font-sans text-mist-900 selection:bg-wisp-100 selection:text-wisp-900"
>
    <!-- Sidebar -->
    <aside class="flex w-64 flex-col border-r border-mist-200 bg-white shadow-sm">
        <!-- Brand -->
        <div
            class="flex h-16 items-center gap-3 border-b border-mist-100 px-5 transition-all hover:bg-mist-50/50"
        >
            <div class="flex h-8 w-8 items-center justify-center rounded-lg">
                <Logo size="lg" />
            </div>
            <div class="flex flex-col leading-none">
                <span class="text-base font-bold tracking-tight text-mist-900">RunWisp</span>
            </div>
        </div>

        <!-- Navigation -->
        <div class="flex-1 overflow-y-auto px-3 py-6">
            <nav class="mb-6 space-y-0.5">
                <a
                    href={resolve(urls.overview)}
                    class="group flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition-all {activePage ===
                    'overview'
                        ? 'bg-wisp-50 text-wisp-700 shadow-sm shadow-wisp-500/5'
                        : 'text-mist-600 hover:bg-mist-50 hover:text-mist-900'}"
                >
                    <Activity
                        size={18}
                        class={activePage === "overview"
                            ? "text-wisp-600"
                            : "text-mist-400 transition-colors group-hover:text-mist-600"}
                    />
                    Overview
                </a>
                <a
                    href={resolve(urls.runs)}
                    class="group flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition-all {activePage ===
                    'runs'
                        ? 'bg-wisp-50 text-wisp-700 shadow-sm shadow-wisp-500/5'
                        : 'text-mist-600 hover:bg-mist-50 hover:text-mist-900'}"
                >
                    <History
                        size={18}
                        class={activePage === "runs"
                            ? "text-wisp-600"
                            : "text-mist-400 transition-colors group-hover:text-mist-600"}
                    />
                    All Runs
                </a>
            </nav>

            {#if showGroupHeaders}
                {#each taskGroups as group (group.name)}
                    <div
                        class="mt-4 mb-2 px-3 text-[11px] font-bold tracking-wider text-mist-400 uppercase first:mt-0"
                    >
                        {group.name}
                    </div>
                    <nav class="mb-2 space-y-0.5">
                        {#each group.tasks as task (task.id)}
                            <a
                                href={resolve(task.href || "#")}
                                class="group flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition-all {activePage ===
                                task.id
                                    ? 'bg-wisp-50 text-wisp-700 shadow-sm shadow-wisp-500/5'
                                    : 'text-mist-600 hover:bg-mist-50 hover:text-mist-900'}"
                            >
                                <Box
                                    size={18}
                                    class={activePage === task.id
                                        ? "text-wisp-600"
                                        : "text-mist-400 transition-colors group-hover:text-mist-600"}
                                />
                                {task.name}
                            </a>
                        {/each}
                    </nav>
                {/each}
            {:else}
                <div class="mb-2 px-3 text-[11px] font-bold tracking-wider text-mist-400 uppercase">
                    Tasks
                </div>
                <nav class="mb-8 space-y-0.5">
                    {#each tasks as task (task.id)}
                        <a
                            href={resolve(task.href || "#")}
                            class="group flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition-all {activePage ===
                            task.id
                                ? 'bg-wisp-50 text-wisp-700 shadow-sm shadow-wisp-500/5'
                                : 'text-mist-600 hover:bg-mist-50 hover:text-mist-900'}"
                        >
                            <Box
                                size={18}
                                class={activePage === task.id
                                    ? "text-wisp-600"
                                    : "text-mist-400 transition-colors group-hover:text-mist-600"}
                            />
                            {task.name}
                        </a>
                    {/each}
                </nav>
            {/if}
        </div>

        <!-- Status Footer -->
        <ConnectionStatusIndicator />
    </aside>

    <!-- Main Content -->
    <main class="flex flex-1 flex-col overflow-hidden">
        <!-- Topbar -->
        <header
            class="flex h-16 items-center justify-between border-b border-mist-200 bg-white px-6 shadow-sm"
        >
            <!-- Breadcrumb / Title -->
            <div class="flex items-center gap-2">
                <span class="text-mist-400">RunWisp</span>
                <span class="text-mist-300">/</span>
                <span class="font-semibold text-mist-900 capitalize"
                    >{activePage.replace("task_", "").replace(/_/g, " ")}</span
                >
            </div>
            <NotificationBell />
        </header>

        <!-- Scrollable Area -->
        <div class="flex-1 overflow-y-auto scroll-smooth p-6">
            {@render children()}
        </div>
    </main>
</div>
