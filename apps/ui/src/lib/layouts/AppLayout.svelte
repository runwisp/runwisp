<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import {
        Activity,
        ArrowLeft,
        RotateCcwClock,
        Menu,
        PanelLeftClose,
        PanelLeftOpen,
        Search,
        X,
    } from "@lucide/svelte";
    import { type Snippet, type Component, flushSync, tick } from "svelte";
    import { resolve } from "$app/paths";
    import { page } from "$app/stores";
    import AuthDisabledBadge from "$lib/components/AuthDisabledBadge.svelte";
    import StationModeBadge from "$lib/components/StationModeBadge.svelte";
    import ConnectionStatusIndicator from "$lib/components/ConnectionStatusIndicator.svelte";
    import FeedbackCard from "$lib/components/FeedbackCard.svelte";
    import HeaderSearch from "$lib/components/HeaderSearch.svelte";
    import NotificationBell from "$lib/components/NotificationBell.svelte";
    import StaleConfigBanner from "$lib/components/StaleConfigBanner.svelte";
    import TaskScheduleChip from "$lib/components/TaskScheduleChip.svelte";
    import TaskUsage from "$lib/components/TaskUsage.svelte";
    import { headerSearchStore, systemStore } from "$lib/stores";
    import { showScheduleChip } from "$lib/utils/task-schedule";
    import { StoredFlag } from "$lib/utils/stored-flag.svelte";
    import { ThemeToggle, Logo } from "@runwisp/ui";
    import type { Task } from "@runwisp/common";

    let {
        activePage,
        activeTask,
        tasks = [],
        tasksLoading = false,
        children,
    }: {
        activePage: string;
        /** The task whose detail page is open, if any. */
        activeTask?: Task | undefined;
        tasks?: {
            id: string;
            name: string;
            group?: string;
            icon: Component;
        }[];
        /** The task list hasn't loaded yet: show placeholders, not an empty list. */
        tasksLoading?: boolean;
        children: Snippet;
    } = $props();

    type TaskGroup = { name: string; tasks: typeof tasks };

    let taskGroups: TaskGroup[] = $derived.by(() => {
        const groups: Record<string, typeof tasks> = {};
        for (const task of tasks) {
            (groups[task.group ?? "Tasks"] ??= []).push(task);
        }
        return Object.entries(groups).map(([name, groupTasks]) => ({ name, tasks: groupTasks }));
    });

    let showGroupHeaders = $derived(taskGroups.length > 1);

    let sidebarOpen = $state(false);
    // Between lg and 3xl the sidebar sits beside the page but can be folded
    // away for room; from 3xl it always shows. Below lg it is the drawer.
    const sidebarHidden = new StoredFlag("runwisp:sidebar-hidden");
    let searchOpen = $state(false);
    let headerSearch = $state<HeaderSearch | null>(null);
    let firstLink = $state<HTMLElement | null>(null);

    let lastPath = $page.url.pathname;
    $effect(() => {
        const path = $page.url.pathname;
        if (path !== lastPath) {
            lastPath = path;
            sidebarOpen = false;
            searchOpen = false;
        }
    });

    // Below md the header has no room for the search pill, so a search button
    // swaps the whole bar for the field, the way phone apps enter "search
    // mode". The back arrow leaves it and drops the query; blurring an empty
    // field leaves it too, while a live query keeps the bar open so the
    // operator can see the list is filtered.

    function openSearch() {
        // Render the field synchronously and focus it inside the tap handler:
        // iOS only raises the keyboard for a focus made during the gesture.
        flushSync(() => (searchOpen = true));
        headerSearch?.focus();
    }

    function closeSearch() {
        headerSearchStore.clear();
        searchOpen = false;
    }

    function onSearchFocusOut(e: FocusEvent) {
        const next = e.relatedTarget;
        if (
            next instanceof Node &&
            e.currentTarget instanceof Node &&
            e.currentTarget.contains(next)
        ) {
            return;
        }
        if (!headerSearchStore.query) searchOpen = false;
    }

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

    // The drawer's open-focus target is the first sidebar link, whichever nav
    // link that happens to be (Overview, unless a sidebar without the static
    // nav is ever composed). An action rather than a special-cased snippet
    // call keeps every link rendered through the same markup.
    function registerFirstLink(node: HTMLElement, isFirst: boolean) {
        if (isFirst) firstLink = node;
    }
</script>

<svelte:window onkeydown={handleKey} />

{#snippet navLink(href: string, active: boolean, Icon: Component, label: string, first = false)}
    <!-- href is always resolve()d by the caller; the lint rule can't trace that through a parameter -->
    <!-- eslint-disable svelte/no-navigation-without-resolve -->
    <a
        {href}
        use:registerFirstLink={first}
        class="group flex items-center gap-3 rounded-[3px] px-3 py-2 font-mono text-sm font-medium {active
            ? 'bg-primary-soft text-primary-soft-text'
            : 'text-on-surface-muted hover:bg-surface-sunken hover:text-primary'}"
    >
        <!-- eslint-enable svelte/no-navigation-without-resolve -->
        <Icon
            size={18}
            class={active
                ? "text-primary"
                : "text-on-surface-faint group-hover:text-on-surface-muted"}
        />
        {label}
    </a>
{/snippet}

<div
    class="flex h-screen w-full bg-surface-sunken font-sans text-on-surface selection:bg-primary-soft selection:text-primary-soft-text"
>
    {#if sidebarOpen}
        <button
            type="button"
            aria-label="Close navigation"
            class="fixed inset-0 z-30 bg-black/40 lg:hidden"
            onclick={closeDrawer}
        ></button>
    {/if}

    <aside
        id="app-sidebar"
        aria-label="Primary"
        class="fixed inset-y-0 left-0 z-40 flex w-64 flex-col border-r border-outline bg-surface-raised transition-transform duration-150 ease-out lg:static lg:translate-x-0 {sidebarOpen
            ? 'translate-x-0'
            : '-translate-x-full'} {sidebarHidden.current ? 'lg:hidden 3xl:flex' : ''}"
    >
        <!-- Brand — the same lockup as the website nav: teal mark at 21px,
             wordmark in the body sans at 700. Brand voice, not chrome, so it
             deliberately stays out of the mono. -->
        <div class="flex h-[52px] items-center gap-[9px] border-b border-outline px-5">
            <Logo size="md" />
            <div class="flex flex-1 flex-col leading-none">
                <span class="font-sans text-[18px] font-bold tracking-[-0.02em] text-on-surface"
                    >RunWisp</span
                >
            </div>
            <button
                type="button"
                aria-label="Close navigation"
                class="rounded-[3px] p-1 text-on-surface-muted hover:bg-surface-sunken hover:text-primary lg:hidden"
                onclick={closeDrawer}
            >
                <X size={18} />
            </button>
        </div>

        <div class="flex-1 overflow-y-auto px-3 py-6">
            <nav class="mb-6 space-y-0.5">
                {@render navLink(
                    resolve("/"),
                    activePage === "overview",
                    Activity,
                    "Overview",
                    true,
                )}
                {@render navLink(
                    resolve("/runs"),
                    activePage === "runs",
                    RotateCcwClock,
                    "All Runs",
                )}
            </nav>

            {#if tasksLoading}
                <div class="mt-4 space-y-0.5" role="status" aria-label="Loading tasks">
                    {#each [0, 1, 2, 3] as i (i)}
                        <div class="flex items-center gap-3 px-3 py-2">
                            <span class="size-[18px] animate-pulse rounded bg-outline-hover"></span>
                            <span
                                class="h-3 animate-pulse rounded bg-outline-hover"
                                style:width="{60 - i * 10}%"
                            ></span>
                        </div>
                    {/each}
                </div>
            {:else if showGroupHeaders}
                {#each taskGroups as group (group.name)}
                    <div
                        class="mt-4 mb-2 px-3 font-mono text-2xs font-medium tracking-[0.16em] text-on-surface-faint uppercase first:mt-0"
                    >
                        {group.name}
                    </div>
                    <nav class="mb-2 space-y-0.5">
                        {#each group.tasks as task (task.id)}
                            {@render navLink(
                                resolve(`/tasks/${task.name}`),
                                activePage === task.id,
                                task.icon,
                                task.name,
                            )}
                        {/each}
                    </nav>
                {/each}
            {:else}
                <div
                    class="mb-2 px-3 font-mono text-2xs font-medium tracking-[0.16em] text-on-surface-faint uppercase"
                >
                    Tasks
                </div>
                <nav class="mb-8 space-y-0.5">
                    {#each tasks as task (task.id)}
                        {@render navLink(
                            resolve(`/tasks/${task.name}`),
                            activePage === task.id,
                            task.icon,
                            task.name,
                        )}
                    {/each}
                </nav>
            {/if}
        </div>

        <FeedbackCard />
        <ConnectionStatusIndicator />
    </aside>

    <main class="flex flex-1 flex-col overflow-hidden">
        <header
            class="@container relative flex h-[52px] items-center justify-between gap-2 border-b border-outline bg-surface-raised px-6"
        >
            <div class="flex min-w-0 items-center gap-3">
                <button
                    type="button"
                    aria-label="Open navigation"
                    aria-expanded={sidebarOpen}
                    aria-controls="app-sidebar"
                    class="-ml-2 rounded-[3px] p-2 text-on-surface-muted hover:bg-surface-sunken hover:text-primary lg:hidden"
                    onclick={openDrawer}
                >
                    <Menu size={20} />
                </button>
                <button
                    type="button"
                    aria-label={sidebarHidden.current ? "Show sidebar" : "Hide sidebar"}
                    title={sidebarHidden.current ? "Show sidebar" : "Hide sidebar"}
                    aria-expanded={!sidebarHidden.current}
                    aria-controls="app-sidebar"
                    class="-ml-2 hidden rounded-[3px] p-2 text-on-surface-muted hover:bg-surface-sunken hover:text-primary lg:block 3xl:hidden"
                    onclick={() => (sidebarHidden.current = !sidebarHidden.current)}
                >
                    {#if sidebarHidden.current}
                        <PanelLeftOpen size={20} />
                    {:else}
                        <PanelLeftClose size={20} />
                    {/if}
                </button>
                <!-- The breadcrumb root is the first thing to go when the bar
                     gets tight; the task name and schedule matter more. -->
                <span class="hidden font-mono text-on-surface-faint @5xl:inline">RunWisp</span>
                <span class="hidden font-mono text-on-surface-faint @5xl:inline">/</span>
                {#if activeTask}
                    <!-- On a task page the breadcrumb is the page's primary heading:
                         the task name appears here and nowhere else. -->
                    <h1 class="min-w-0 truncate font-mono text-base font-extrabold text-on-surface">
                        {activeTask.name}
                    </h1>
                    {#if showScheduleChip(activeTask, systemStore.schedulingActive)}
                        <TaskScheduleChip task={activeTask} />
                    {/if}
                    <span class="hidden shrink-0 @2xl:inline">
                        <TaskUsage task={activeTask} />
                    </span>
                {:else}
                    <span class="font-mono font-semibold text-on-surface capitalize"
                        >{activePage.replace("task_", "").replace(/_/g, " ")}</span
                    >
                {/if}
            </div>

            <!-- Center: page search (filters the run list / searches log output).
                 Empty space when the active page registers no search. On phones
                 it is hidden until the search button opens it over the bar. -->
            <div
                class="{searchOpen
                    ? 'absolute inset-0 z-10 flex items-center gap-2 bg-surface-raised px-4'
                    : 'hidden'} md:static md:z-auto md:flex md:min-w-24 md:flex-1 md:justify-center md:bg-transparent md:px-4 lg:px-8"
                onfocusout={onSearchFocusOut}
            >
                {#if searchOpen}
                    <button
                        type="button"
                        aria-label="Close search"
                        class="shrink-0 rounded-[3px] p-2 text-on-surface-muted hover:bg-surface-sunken hover:text-primary md:hidden"
                        onclick={closeSearch}
                    >
                        <ArrowLeft size={20} />
                    </button>
                {/if}
                <HeaderSearch bind:this={headerSearch} />
            </div>

            <div class="flex shrink-0 items-center gap-2 sm:gap-3">
                {#if headerSearchStore.active}
                    <button
                        type="button"
                        aria-label="Search"
                        class="rounded-[3px] p-2 text-on-surface-muted hover:bg-surface-sunken hover:text-primary md:hidden"
                        onclick={openSearch}
                    >
                        <Search size={18} />
                    </button>
                {/if}
                <StationModeBadge />
                <AuthDisabledBadge />
                <ThemeToggle />
                <NotificationBell />
            </div>
        </header>

        <StaleConfigBanner />

        <div class="flex-1 overflow-y-auto scroll-smooth p-6">
            {@render children()}
        </div>
    </main>
</div>
