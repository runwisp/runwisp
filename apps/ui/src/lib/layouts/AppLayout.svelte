<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { Activity, History, Globe, Menu, X } from "@lucide/svelte";
    import Logo from "@runwisp/ui/components/Logo.svelte";
    import { type Snippet, type Component, tick } from "svelte";
    import { resolve } from "$app/paths";
    import { page } from "$app/stores";
    import ConnectionStatusIndicator from "$lib/components/ConnectionStatusIndicator.svelte";
    import ConnectionPip from "$lib/components/ConnectionPip.svelte";
    import NotificationBell from "$lib/components/NotificationBell.svelte";
    import ThemeToggle from "$lib/components/ThemeToggle.svelte";
    import { systemStore } from "$lib/stores/system.svelte";

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
        tasks?: { id: string; name: string; group?: string; icon: Component; href?: string }[];
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

    let sidebarOpen = $state(false);
    let firstLink = $state<HTMLElement | null>(null);

    let lastPath = $page.url.pathname;
    $effect(() => {
        const path = $page.url.pathname;
        if (path !== lastPath) {
            lastPath = path;
            sidebarOpen = false;
        }
    });

    async function openDrawer() {
        sidebarOpen = true;
        await tick();
        firstLink?.focus();
    }

    function closeDrawer() {
        sidebarOpen = false;
    }

    function handleKey(e: KeyboardEvent) {
        if (e.key === "Escape" && sidebarOpen) {
            e.preventDefault();
            closeDrawer();
        }
    }
</script>

<svelte:window onkeydown={handleKey} />

<div
    class="flex h-screen w-full bg-surface-sunken font-sans text-on-surface selection:bg-primary-soft selection:text-primary-soft-text"
>
    {#if sidebarOpen}
        <button
            type="button"
            aria-label="Close navigation"
            class="fixed inset-0 z-30 bg-black/40 md:hidden"
            onclick={closeDrawer}
        ></button>
    {/if}

    <!-- Sidebar -->
    <aside
        id="app-sidebar"
        aria-label="Primary"
        class="duration-normal fixed inset-y-0 left-0 z-40 flex w-64 flex-col border-r border-outline bg-surface-raised shadow-sm transition-transform ease-out md:static md:translate-x-0 {sidebarOpen
            ? 'translate-x-0'
            : '-translate-x-full'}"
    >
        <!-- Brand -->
        <div
            class="flex h-16 items-center gap-3 border-b border-outline-faint px-5 transition-all hover:bg-surface-sunken/50"
        >
            <div class="flex h-8 w-8 items-center justify-center rounded-lg">
                <Logo size="lg" />
            </div>
            <div class="flex flex-1 flex-col leading-none">
                <span class="text-base font-bold tracking-tight text-on-surface">RunWisp</span>
            </div>
            <button
                type="button"
                aria-label="Close navigation"
                class="rounded-md p-1 text-on-surface-muted hover:bg-surface-sunken hover:text-on-surface md:hidden"
                onclick={closeDrawer}
            >
                <X size={18} />
            </button>
        </div>

        <!-- Navigation -->
        <div class="flex-1 overflow-y-auto px-3 py-6">
            <nav class="mb-6 space-y-0.5">
                <a
                    bind:this={firstLink}
                    href={resolve(urls.overview)}
                    class="group flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition-all {activePage ===
                    'overview'
                        ? 'bg-primary-soft text-primary-soft-text shadow-sm shadow-wisp-500/5'
                        : 'text-on-surface-muted hover:bg-surface-sunken hover:text-on-surface'}"
                >
                    <Activity
                        size={18}
                        class={activePage === "overview"
                            ? "text-primary"
                            : "text-on-surface-faint transition-colors group-hover:text-on-surface-muted"}
                    />
                    Overview
                </a>
                <a
                    href={resolve(urls.runs)}
                    class="group flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition-all {activePage ===
                    'runs'
                        ? 'bg-primary-soft text-primary-soft-text shadow-sm shadow-wisp-500/5'
                        : 'text-on-surface-muted hover:bg-surface-sunken hover:text-on-surface'}"
                >
                    <History
                        size={18}
                        class={activePage === "runs"
                            ? "text-primary"
                            : "text-on-surface-faint transition-colors group-hover:text-on-surface-muted"}
                    />
                    All Runs
                </a>
            </nav>

            {#if showGroupHeaders}
                {#each taskGroups as group (group.name)}
                    <div
                        class="mt-4 mb-2 px-3 text-2xs font-bold tracking-wider text-on-surface-faint uppercase first:mt-0"
                    >
                        {group.name}
                    </div>
                    <nav class="mb-2 space-y-0.5">
                        {#each group.tasks as task (task.id)}
                            {@const TaskIcon = task.icon}
                            <a
                                href={resolve(task.href || "#")}
                                class="group flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition-all {activePage ===
                                task.id
                                    ? 'bg-primary-soft text-primary-soft-text shadow-sm shadow-wisp-500/5'
                                    : 'text-on-surface-muted hover:bg-surface-sunken hover:text-on-surface'}"
                            >
                                <TaskIcon
                                    size={18}
                                    class={activePage === task.id
                                        ? "text-primary"
                                        : "text-on-surface-faint transition-colors group-hover:text-on-surface-muted"}
                                />
                                {task.name}
                            </a>
                        {/each}
                    </nav>
                {/each}
            {:else}
                <div
                    class="mb-2 px-3 text-2xs font-bold tracking-wider text-on-surface-faint uppercase"
                >
                    Tasks
                </div>
                <nav class="mb-8 space-y-0.5">
                    {#each tasks as task (task.id)}
                        {@const TaskIcon = task.icon}
                        <a
                            href={resolve(task.href || "#")}
                            class="group flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition-all {activePage ===
                            task.id
                                ? 'bg-primary-soft text-primary-soft-text shadow-sm shadow-wisp-500/5'
                                : 'text-on-surface-muted hover:bg-surface-sunken hover:text-on-surface'}"
                        >
                            <TaskIcon
                                size={18}
                                class={activePage === task.id
                                    ? "text-primary"
                                    : "text-on-surface-faint transition-colors group-hover:text-on-surface-muted"}
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
            class="flex h-16 items-center justify-between border-b border-outline bg-surface-raised px-6 shadow-sm"
        >
            <!-- Breadcrumb / Title -->
            <div class="flex items-center gap-3">
                <button
                    type="button"
                    aria-label="Open navigation"
                    aria-expanded={sidebarOpen}
                    aria-controls="app-sidebar"
                    class="-ml-2 rounded-md p-2 text-on-surface-muted hover:bg-surface-sunken hover:text-on-surface md:hidden"
                    onclick={openDrawer}
                >
                    <Menu size={20} />
                </button>
                <span class="hidden text-on-surface-faint sm:inline">RunWisp</span>
                <span class="hidden text-on-surface-faint sm:inline">/</span>
                <span class="font-semibold text-on-surface capitalize"
                    >{activePage.replace("task_", "").replace(/_/g, " ")}</span
                >
            </div>
            <div class="flex items-center gap-3">
                {#if systemStore.timezone}
                    <span
                        class="flex items-center gap-1.5 rounded-full border border-outline bg-surface-sunken px-2.5 py-1 text-xs font-medium text-on-surface-muted"
                        title={systemStore.timezoneSource === "system"
                            ? "Detected from the host system; pin [scheduler] timezone in runwisp.toml to make it explicit."
                            : "Set in runwisp.toml under [scheduler] timezone."}
                    >
                        <Globe size={12} class="text-on-surface-faint" />
                        {systemStore.timezone}
                        {#if systemStore.timezoneSource}
                            <span class="text-on-surface-faint">({systemStore.timezoneSource})</span
                            >
                        {/if}
                    </span>
                {/if}
                <ConnectionPip />
                <ThemeToggle />
                <NotificationBell />
            </div>
        </header>

        <!-- Scrollable Area -->
        <div class="flex-1 overflow-y-auto scroll-smooth p-6">
            {@render children()}
        </div>
    </main>
</div>
