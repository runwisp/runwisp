<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { Activity, RotateCcwClock, Menu, X } from "@lucide/svelte";
    import { type Snippet, type Component, tick } from "svelte";
    import { resolve } from "$app/paths";
    import { page } from "$app/stores";
    import AuthDisabledBadge from "$lib/components/AuthDisabledBadge.svelte";
    import StationModeBadge from "$lib/components/StationModeBadge.svelte";
    import ConnectionStatusIndicator from "$lib/components/ConnectionStatusIndicator.svelte";
    import FeedbackCard from "$lib/components/FeedbackCard.svelte";
    import HeaderSearch from "$lib/components/HeaderSearch.svelte";
    import NotificationBell from "$lib/components/NotificationBell.svelte";
    import StaleConfigBanner from "$lib/components/StaleConfigBanner.svelte";
    import { ThemeToggle, Logo } from "@runwisp/ui";

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

    // On a task detail page `activePage` is the task's page id; resolve it back to
    // the real task name so the breadcrumb shows the literal name (its only home).
    let activeTaskName = $derived(
        tasks.find((t: { id: string; name: string }) => t.id === activePage)?.name,
    );

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
            class="fixed inset-0 z-30 bg-black/40 md:hidden"
            onclick={closeDrawer}
        ></button>
    {/if}

    <aside
        id="app-sidebar"
        aria-label="Primary"
        class="fixed inset-y-0 left-0 z-40 flex w-64 flex-col border-r border-outline bg-surface-raised transition-transform duration-150 ease-out md:static md:translate-x-0 {sidebarOpen
            ? 'translate-x-0'
            : '-translate-x-full'}"
    >
        <!-- Brand — the same lockup as the website nav: teal mark at 21px,
             wordmark in the body sans at 700. Brand voice, not chrome, so it
             deliberately stays out of the mono. -->
        <div
            class="flex h-[52px] items-center gap-[9px] border-b border-outline px-5 hover:bg-surface-sunken/50"
        >
            <Logo size="md" />
            <div class="flex flex-1 flex-col leading-none">
                <span class="font-sans text-[18px] font-bold tracking-[-0.02em] text-on-surface"
                    >RunWisp</span
                >
            </div>
            <button
                type="button"
                aria-label="Close navigation"
                class="rounded-[3px] p-1 text-on-surface-muted hover:bg-surface-sunken hover:text-primary md:hidden"
                onclick={closeDrawer}
            >
                <X size={18} />
            </button>
        </div>

        <div class="flex-1 overflow-y-auto px-3 py-6">
            <nav class="mb-6 space-y-0.5">
                {@render navLink(
                    resolve(urls.overview),
                    activePage === "overview",
                    Activity,
                    "Overview",
                    true,
                )}
                {@render navLink(
                    resolve(urls.runs),
                    activePage === "runs",
                    RotateCcwClock,
                    "All Runs",
                )}
            </nav>

            {#if showGroupHeaders}
                {#each taskGroups as group (group.name)}
                    <div
                        class="mt-4 mb-2 px-3 font-mono text-2xs font-medium tracking-[0.16em] text-on-surface-faint uppercase first:mt-0"
                    >
                        {group.name}
                    </div>
                    <nav class="mb-2 space-y-0.5">
                        {#each group.tasks as task (task.id)}
                            {@render navLink(
                                resolve(task.href || "#"),
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
                            resolve(task.href || "#"),
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
            class="flex h-[52px] items-center justify-between border-b border-outline bg-surface-raised px-6"
        >
            <div class="flex min-w-0 items-center gap-3">
                <button
                    type="button"
                    aria-label="Open navigation"
                    aria-expanded={sidebarOpen}
                    aria-controls="app-sidebar"
                    class="-ml-2 rounded-[3px] p-2 text-on-surface-muted hover:bg-surface-sunken hover:text-primary md:hidden"
                    onclick={openDrawer}
                >
                    <Menu size={20} />
                </button>
                <span class="hidden font-mono text-on-surface-faint sm:inline">RunWisp</span>
                <span class="hidden font-mono text-on-surface-faint sm:inline">/</span>
                {#if activeTaskName}
                    <!-- On a task page the breadcrumb is the page's primary heading:
                         the task name appears here and nowhere else. -->
                    <h1 class="min-w-0 truncate font-mono text-base font-extrabold text-on-surface">
                        {activeTaskName}
                    </h1>
                {:else}
                    <span class="font-mono font-semibold text-on-surface capitalize"
                        >{activePage.replace("task_", "").replace(/_/g, " ")}</span
                    >
                {/if}
            </div>

            <!-- Center: page search (filters the run list / searches log output).
                 Empty space when the active page registers no search. -->
            <div class="hidden min-w-0 flex-1 justify-center px-4 md:flex lg:px-8">
                <HeaderSearch />
            </div>

            <div class="flex shrink-0 items-center gap-2 sm:gap-3">
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
