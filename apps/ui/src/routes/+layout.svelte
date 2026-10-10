<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import "../app.css";
    import { page } from "$app/stores";
    import { preloadCode } from "$app/navigation";
    import { untrack } from "svelte";
    import {
        runUpdatesStore,
        authStore,
        taskStore,
        notificationStore,
        appEventStream,
        connectionStore,
    } from "$lib/stores";
    import { systemStore } from "$lib/stores/system.svelte";
    import AuthModal from "$lib/components/AuthModal.svelte";
    import AppLayout from "$lib/layouts/AppLayout.svelte";
    import { ToastContainer } from "@runwisp/ui";
    import { taskIcon } from "$lib/utils/task";

    let { children } = $props();

    function disconnectStores() {
        runUpdatesStore.disconnect();
        notificationStore.disconnect();
        systemStore.disconnect();
    }

    $effect(() => {
        // Best-effort: preload route JS so a click still navigates when the
        // daemon (which serves the chunks) has since gone down.
        void preloadCode("/");
        void preloadCode("/runs");

        void authStore.load();

        return disconnectStores;
    });

    $effect(() => {
        const status = authStore.current;
        if (!status.loaded || !status.authenticated) {
            // A mid-session logout (401 on any REST call flips authStore to
            // unauthenticated) re-runs this effect into this branch. Without
            // tearing the stores down here they keep applying SSE-pushed state
            // underneath the auth modal until the whole page unmounts.
            disconnectStores();
            return;
        }

        runUpdatesStore.connect();
        void taskStore.loadIfNeeded();
        void notificationStore.init();
        // Seed system identity + stats once, then ride the shared app-event
        // stream for live cpu/mem/uptime and config-staleness, no polling.
        void systemStore.init();
    });

    let activePage = $derived.by(() => {
        const path = $page.url.pathname;
        if (path === "/") return "overview";
        if (path.startsWith("/runs")) return "runs";
        return "";
    });

    let activeTaskName = $derived(
        $page.url.pathname.startsWith("/tasks/") ? $page.params.id : undefined,
    );

    let activeTask = $derived(taskStore.items.find((t) => t.name === activeTaskName));

    let navTasks = $derived(
        taskStore.items.map((t) => ({
            name: t.name,
            group: t.group ?? "Tasks",
            icon: taskIcon(t),
        })),
    );

    let isAuthenticated = $derived(authStore.current.authenticated);

    // The daemon announces task-set changes (a reload, a schedule pause) on the
    // app stream; refetch so the sidebar and top bar follow without a page
    // reload. A reconnect may have missed one.
    $effect(() => {
        if (!isAuthenticated) return;
        return untrack(() => {
            const offChanged = appEventStream.subscribe("tasks.changed", () => {
                void taskStore.refresh();
            });
            const offReconnect = connectionStore.onReconnect(() => void taskStore.refresh());
            return () => {
                offChanged();
                offReconnect();
            };
        });
    });
</script>

<svelte:head>
    <title>{systemStore.title()}</title>
    <meta
        name="description"
        content="Web-based task scheduling and process supervision with real-time monitoring"
    />
</svelte:head>

<AuthModal />
<ToastContainer />

{#if isAuthenticated}
    <AppLayout
        {activePage}
        {activeTaskName}
        {activeTask}
        tasks={navTasks}
        tasksLoading={!taskStore.loaded && !taskStore.loadFailed}
    >
        {@render children()}
    </AppLayout>
{:else if !authStore.current.loaded}
    <div class="flex h-screen items-center justify-center bg-surface-sunken">
        <div
            class="h-8 w-8 animate-spin rounded-full border-4 border-primary border-t-transparent"
        ></div>
    </div>
{:else}
    <div class="h-screen bg-surface-sunken"></div>
{/if}
