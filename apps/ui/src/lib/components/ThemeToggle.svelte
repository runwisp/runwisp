<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { Monitor, Sun, Moon, ChevronDown } from "@lucide/svelte";
    import { Dropdown } from "@runwisp/ui";
    import { themeStore, type ThemePreference } from "$lib/stores";

    const options: { value: ThemePreference; label: string; icon: typeof Monitor }[] = [
        { value: "auto", label: "Auto", icon: Monitor },
        { value: "light", label: "Light", icon: Sun },
        { value: "dark", label: "Dark", icon: Moon },
    ];

    let TriggerIcon = $derived(
        themeStore.preference === "auto" ? Monitor : themeStore.resolved === "dark" ? Moon : Sun,
    );

    let items = $derived(
        options.map((o) => ({
            label: o.label,
            icon: o.icon,
            selected: themeStore.preference === o.value,
            onClick: () => themeStore.set(o.value),
        })),
    );
</script>

<Dropdown {items} align="right" triggerLabel="Theme">
    {#snippet trigger()}
        <span class="flex items-center gap-1">
            <TriggerIcon size={18} />
            <ChevronDown size={14} class="opacity-60" />
        </span>
    {/snippet}
</Dropdown>
