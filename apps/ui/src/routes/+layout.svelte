<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import "../app.css";
    import { browser } from "$app/environment";
    import { page } from "$app/stores";
    import { preloadCode } from "$app/navigation";
    import { runUpdatesStore, authStore, taskStore, notificationStore } from "$lib/stores";
    import { systemStore } from "$lib/stores/system.svelte";
    import AuthModal from "$lib/components/AuthModal.svelte";
    import AppLayout from "$lib/layouts/AppLayout.svelte";
    import { ToastContainer } from "@runwisp/ui";
    import { toTaskPageId } from "$lib/utils/task-id";
    import { taskIcon } from "$lib/utils/task-icon";

    let { children } = $props();

    let hydrated = $state(false);

    $effect(() => {
        hydrated = true;
        if (!browser) return;

        // Best-effort: preload route JS so a click still navigates when the
        // daemon (which serves the chunks) has since gone down.
        void preloadCode("/");
        void preloadCode("/runs");

        void authStore.load();

        return () => {
            runUpdatesStore.disconnect();
            notificationStore.disconnect();
            systemStore.disconnect();
        };
    });

    $effect(() => {
        const status = authStore.current;
        if (!status.loaded || !status.authenticated) return;

        runUpdatesStore.connect();
        void taskStore.loadIfNeeded();
        void notificationStore.init();
        // Seed system identity + stats once, then ride the shared app-event
        // stream for live cpu/mem/uptime and config-staleness — no polling.
        void systemStore.init();
    });

    let activePage = $derived.by(() => {
        const path = $page.url.pathname;
        if (path === "/") return "overview";
        if (path.startsWith("/runs")) return "runs";
        if (path.startsWith("/tasks/")) {
            const parts = path.split("/");
            return parts[2] ? toTaskPageId(parts[2]) : "";
        }
        return "";
    });

    let navTasks = $derived(
        taskStore.items.map((t) => ({
            id: toTaskPageId(t.name),
            name: t.name,
            group: t.group ?? "Tasks",
            href: `/tasks/${t.name}`,
            icon: taskIcon(t),
        })),
    );

    let isAuthenticated = $derived(!hydrated ? false : authStore.current.authenticated);
</script>

<svelte:head>
    <title>RunWisp</title>
    <meta
        name="description"
        content="Web-based task scheduling and process supervision with real-time monitoring"
    />
</svelte:head>

<AuthModal />
<ToastContainer />

{#if isAuthenticated}
    <AppLayout {activePage} tasks={navTasks} urls={{ overview: "/", runs: "/runs" }}>
        {@render children()}
    </AppLayout>
{:else if !hydrated || !authStore.current.loaded}
    <div class="flex h-screen items-center justify-center bg-surface-sunken">
        <div
            class="h-8 w-8 animate-spin rounded-full border-4 border-primary border-t-transparent"
        ></div>
    </div>
{:else}
    <div class="h-screen bg-surface-sunken"></div>
{/if}
