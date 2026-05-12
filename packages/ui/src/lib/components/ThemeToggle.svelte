<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { Moon, Sun } from "@lucide/svelte";

    interface Props {
        storageKey?: string;
        class?: string;
    }

    let { storageKey = "runwisp:theme", class: className = "" }: Props = $props();

    let isDark = $state(false);
    let mounted = $state(false);

    $effect(() => {
        isDark = document.documentElement.classList.contains("dark");
        mounted = true;
    });

    function toggle(): void {
        const next = !isDark;
        isDark = next;
        document.documentElement.classList.toggle("dark", next);
        try {
            localStorage.setItem(storageKey, next ? "dark" : "light");
        } catch {
            // localStorage may be unavailable (private mode, embedded contexts) — fall through.
        }
    }
</script>

<button
    type="button"
    onclick={toggle}
    aria-pressed={isDark}
    aria-label={isDark ? "Switch to light theme" : "Switch to dark theme"}
    title={isDark ? "Switch to light theme" : "Switch to dark theme"}
    class="inline-flex h-9 w-9 items-center justify-center rounded-lg border border-outline bg-surface-raised text-on-surface-muted transition-colors hover:border-outline-hover hover:text-on-surface {className}"
>
    {#if mounted && isDark}
        <Sun size={16} />
    {:else}
        <Moon size={16} />
    {/if}
</button>
