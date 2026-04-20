<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";
    import type { Component } from "svelte";

    interface Tab {
        id: string;
        label: string;
        icon?: Component;
        badge?: string | number;
        dirty?: boolean;
        disabled?: boolean;
    }

    interface Props {
        tabs: Tab[];
        activeTab?: string;
        onChange?: (tabId: string) => void;
        variant?: "underline" | "pills" | "enclosed";
        fullWidth?: boolean;
        children?: Snippet<[string]>;
        class?: string;
    }

    let {
        tabs,
        activeTab = $bindable(),
        onChange,
        variant = "underline",
        fullWidth = false,
        children,
        class: className = "",
    }: Props = $props();

    $effect(() => {
        if (!activeTab && tabs.length > 0) {
            const firstId = tabs[0]?.id;
            if (firstId) activeTab = firstId;
        }
    });

    const renderedActiveTab = $derived(activeTab ?? tabs[0]?.id ?? "");

    function handleTabClick(tabId: string) {
        if (tabs.find((t) => t.id === tabId)?.disabled) return;
        activeTab = tabId;
        onChange?.(tabId);
    }

    function getTabButtonClasses(tab: Tab): string {
        const baseClasses = `
            ${fullWidth ? "flex-1" : ""}
            inline-flex items-center justify-center gap-2 px-4 py-2.5
            text-sm font-medium
            transition-colors duration-150
            disabled:cursor-not-allowed disabled:opacity-50
        `;

        const isActive = activeTab === tab.id;

        if (variant === "underline") {
            return `${baseClasses} -mb-px border-b-2 ${
                isActive
                    ? "border-primary text-primary-soft-text"
                    : "border-transparent text-on-surface-muted hover:border-outline-hover hover:text-on-surface"
            }`;
        }

        if (variant === "pills" || variant === "enclosed") {
            return `${baseClasses} rounded-lg ${
                isActive
                    ? "bg-surface-raised text-on-surface shadow-sm"
                    : "text-on-surface-muted hover:text-on-surface"
            }`;
        }

        return baseClasses;
    }
</script>

<div class={className}>
    <!-- Tab list -->
    <div
        class="
			{variant === 'underline' ? 'border-b border-outline' : ''}
			{variant === 'pills' ? 'rounded-xl bg-surface-sunken p-1' : ''}
			{variant === 'enclosed' ? 'rounded-xl border border-outline bg-surface-sunken p-1' : ''}
		"
        role="tablist"
    >
        <nav class="flex {fullWidth ? '' : 'gap-1'}" aria-label="Tabs">
            {#each tabs as tab (tab.id)}
                <button
                    onclick={() => handleTabClick(tab.id)}
                    class={getTabButtonClasses(tab)}
                    role="tab"
                    aria-selected={activeTab === tab.id}
                    aria-controls="tabpanel-{tab.id}"
                    disabled={tab.disabled}
                >
                    {#if tab.icon}
                        {@const Icon = tab.icon}
                        <Icon
                            size={16}
                            class={activeTab === tab.id ? "text-primary" : "text-on-surface-faint"}
                        />
                    {/if}
                    <span>{tab.label}</span>
                    {#if tab.dirty}
                        <span
                            class="h-1.5 w-1.5 rounded-full {activeTab === tab.id
                                ? 'bg-warning-surface'
                                : 'bg-on-surface-faint'}"
                            aria-label="Unsaved changes"
                            title="Unsaved changes"
                        ></span>
                    {/if}
                    {#if tab.badge || tab.badge === 0}
                        <span
                            class="
							rounded-full px-1.5 py-0.5 text-xs font-medium
							{activeTab === tab.id
                                ? 'bg-primary-soft text-primary-soft-text'
                                : 'bg-surface-sunken text-on-surface-muted'}
						"
                        >
                            {tab.badge}
                        </span>
                    {/if}
                </button>
            {/each}
        </nav>
    </div>

    <!-- Tab panels -->
    {#if children && renderedActiveTab}
        <div
            id="tabpanel-{renderedActiveTab}"
            role="tabpanel"
            aria-labelledby="tab-{renderedActiveTab}"
            class="mt-4"
        >
            {@render children(renderedActiveTab)}
        </div>
    {/if}
</div>
