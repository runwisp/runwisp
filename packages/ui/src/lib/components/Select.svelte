<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script module lang="ts">
    import type { Component } from "svelte";

    export interface SelectOption {
        value: unknown;
        label: string;
        description?: string | undefined;
        icon?: Component | undefined;
        disabled?: boolean | undefined;
        group?: string | undefined;
        [key: string]: unknown;
    }
</script>

<script lang="ts">
    import { tick, onDestroy } from "svelte";
    import { ChevronDown, Check, Search } from "@lucide/svelte";
    import { computePosition, autoUpdate, flip, shift, offset } from "@floating-ui/dom";
    import { generateUlid } from "@runwisp/common";

    type SelectSize = "sm" | "md" | "lg";

    interface Props {
        value?: unknown;
        options?: SelectOption[];
        label?: string;
        placeholder?: string;
        disabled?: boolean;
        searchable?: boolean;
        size?: SelectSize;
        error?: string;
        hint?: string;
        class?: string;
        id?: string;
        name?: string;
        onchange?: (value: unknown) => void;
    }

    let {
        value = $bindable(),
        options = [],
        label,
        placeholder = "Select...",
        disabled = false,
        searchable = false,
        size = "md",
        error,
        hint,
        class: className = "",
        id = `select-${generateUlid()}`,
        name,
        onchange,
        onsearch,
    }: Props & { onsearch?: (query: string) => void } = $props();

    let isOpen = $state(false);
    let searchQuery = $state("");
    let triggerEl = $state<HTMLElement | null>(null);
    let menuEl = $state<HTMLElement | null>(null);
    let searchInputEl = $state<HTMLInputElement | null>(null);
    let focusedIndex = $state(-1);
    let cleanupFloating = $state<(() => void) | null>(null);

    $effect(() => {
        if (searchable && onsearch) {
            onsearch(searchQuery);
        }
    });

    const selectedOption = $derived(options.find((o) => o.value === value));

    const filteredOptions = $derived.by(() => {
        if (onsearch) return options; // If external search, trust options are already filtered/updated
        if (!searchable || !searchQuery) return options;
        const q = searchQuery.toLowerCase();
        return options.filter(
            (o) =>
                o.label.toLowerCase().includes(q) ||
                o.description?.toLowerCase().includes(q) ||
                o.group?.toLowerCase().includes(q),
        );
    });

    const groupedOptions = $derived.by(() => {
        const groups: Record<string, SelectOption[]> = {};
        const noGroup: SelectOption[] = [];

        filteredOptions.forEach((opt) => {
            const group = opt.group;
            if (group) {
                if (!groups[group]) groups[group] = [];
                groups[group]!.push(opt);
            } else {
                noGroup.push(opt);
            }
        });

        return { groups, noGroup };
    });

    const flattenedVisibleOptions = $derived.by(() => {
        const list: SelectOption[] = [...groupedOptions.noGroup];
        Object.values(groupedOptions.groups).forEach((g) => list.push(...g));
        return list;
    });

    import { portal } from "../actions/portal.js";

    async function setupFloating() {
        if (!triggerEl || !menuEl) return;

        menuEl.style.width = `${triggerEl.offsetWidth}px`;

        cleanupFloating = autoUpdate(triggerEl, menuEl, () => {
            if (!triggerEl || !menuEl) return;
            void computePosition(triggerEl, menuEl, {
                placement: "bottom-start",
                middleware: [offset(6), flip(), shift({ padding: 10 })],
            }).then(({ x, y }) => {
                if (menuEl) {
                    Object.assign(menuEl.style, {
                        left: `${x}px`,
                        top: `${y}px`,
                        position: "absolute",
                    });
                }
            });
        });
    }

    function toggle() {
        if (disabled) return;
        isOpen = !isOpen;
        if (isOpen) {
            searchQuery = "";
            focusedIndex = flattenedVisibleOptions.findIndex((o) => o.value === value);
            if (focusedIndex === -1) focusedIndex = 0;

            void tick().then(() => {
                void setupFloating();
                if (searchable && searchInputEl) {
                    searchInputEl.focus();
                }
            });
        } else {
            cleanupFloating?.();
            cleanupFloating = null;
        }
    }

    function select(option: SelectOption) {
        if (option.disabled) return;
        value = option.value;
        onchange?.(value);
        isOpen = false;
        cleanupFloating?.();
    }

    function handleKeydown(e: KeyboardEvent) {
        if (!isOpen) {
            if (e.key === "Enter" || e.key === " " || e.key === "ArrowDown") {
                e.preventDefault();
                toggle();
            }
            return;
        }

        switch (e.key) {
            case "Escape":
                isOpen = false;
                cleanupFloating?.();
                triggerEl?.focus();
                break;
            case "ArrowDown":
                e.preventDefault();
                focusedIndex = (focusedIndex + 1) % flattenedVisibleOptions.length;
                scrollToOption(focusedIndex);
                break;
            case "ArrowUp":
                e.preventDefault();
                focusedIndex =
                    (focusedIndex - 1 + flattenedVisibleOptions.length) %
                    flattenedVisibleOptions.length;
                scrollToOption(focusedIndex);
                break;
            case "Enter":
                e.preventDefault();
                if (focusedIndex >= 0 && focusedIndex < flattenedVisibleOptions.length) {
                    const selectedOpt = flattenedVisibleOptions[focusedIndex];
                    if (selectedOpt) select(selectedOpt);
                }
                break;
        }
    }

    function scrollToOption(index: number) {
        const el = menuEl?.querySelector(`[data-index="${index}"]`);
        if (!el || !menuEl) return;

        const menuRect = menuEl.getBoundingClientRect();
        const optionRect = el.getBoundingClientRect();

        if (optionRect.bottom > menuRect.bottom) {
            el.scrollIntoView({ block: "end", behavior: "smooth" });
        } else if (optionRect.top < menuRect.top) {
            el.scrollIntoView({ block: "start", behavior: "smooth" });
        }
    }

    function handleOutsideClick(e: MouseEvent) {
        if (!isOpen) return;
        const path = e.composedPath();
        if (triggerEl && path.includes(triggerEl)) return;
        if (menuEl && path.includes(menuEl)) return;
        isOpen = false;
        cleanupFloating?.();
    }

    onDestroy(() => {
        cleanupFloating?.();
    });

    const sizeClasses = {
        sm: "px-2.5 py-1.5 text-xs",
        md: "px-3.5 py-2 text-sm",
        lg: "px-4 py-2.5 text-base",
    };

    const triggerBase = `
        relative w-full text-left cursor-pointer font-mono
        rounded-[3px] border bg-surface-raised
                flex items-center justify-between
        focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2 focus:border-ring
    `;

    const borderClass = $derived(
        error
            ? "border-danger-surface"
            : isOpen
              ? "border-ring ring-2 ring-ring ring-offset-2"
              : "border-outline hover:border-outline-hover shadow-sm hover:shadow-md",
    );
</script>

<svelte:window onclick={handleOutsideClick} />

<div class="relative w-full {className}">
    {#if label}
        <label for={id} class="mb-1.5 block font-mono text-xs font-medium text-on-surface-muted">
            {label}
        </label>
    {/if}

    <button
        type="button"
        bind:this={triggerEl}
        {id}
        {disabled}
        onclick={toggle}
        onkeydown={handleKeydown}
        class="
            {triggerBase}
            {sizeClasses[size]}
            {borderClass}
            {disabled
            ? 'cursor-not-allowed bg-surface-sunken text-on-surface-faint shadow-none'
            : ''}
        "
        aria-haspopup="listbox"
        aria-expanded={isOpen}
    >
        <div class="flex items-center gap-2 truncate pr-6">
            {#if selectedOption?.icon}
                <div class="text-on-surface-muted">
                    <selectedOption.icon size={size === "sm" ? 14 : 18} />
                </div>
            {/if}
            {#if selectedOption}
                <span class="truncate font-medium text-on-surface">
                    {selectedOption.label}
                </span>
            {:else}
                <span class="truncate text-on-surface-faint">{placeholder}</span>
            {/if}
        </div>

        <div
            class="pointer-events-none absolute top-1/2 right-3 -translate-y-1/2 text-on-surface-faint"
        >
            <ChevronDown size={16} class={isOpen ? "rotate-180" : ""} />
        </div>
    </button>

    {#if name}
        <input type="hidden" {name} {value} />
    {/if}

    {#if isOpen}
        <div use:portal bind:this={menuEl} class="z-[9999] min-w-[200px]">
            <div
                class="
                overflow-hidden rounded-[4px] border border-outline bg-surface-overlay shadow-md
            "
            >
                {#if searchable}
                    <div class="border-b border-outline-faint bg-surface-sunken/50 p-2">
                        <div class="relative">
                            <Search
                                size={14}
                                class="absolute top-1/2 left-2.5 -translate-y-1/2 text-on-surface-faint"
                            />
                            <input
                                bind:this={searchInputEl}
                                bind:value={searchQuery}
                                onkeydown={handleKeydown}
                                placeholder="Search..."
                                class="
                                    w-full rounded-[3px] border border-outline bg-surface-raised py-1.5 pr-3 pl-8 font-mono text-xs
                                    text-on-surface placeholder:text-on-surface-faint focus:border-ring focus:ring-2
                                    focus:ring-ring focus:outline-none
                                "
                            />
                        </div>
                    </div>
                {/if}

                <div
                    class="max-h-60 scrollbar-thin scrollbar-thumb-mist-200 scrollbar-track-transparent overflow-x-hidden overflow-y-auto p-1"
                >
                    {#if flattenedVisibleOptions.length === 0}
                        <div class="px-4 py-8 text-center text-sm text-on-surface-muted">
                            No results found
                        </div>
                    {/if}

                    {#each groupedOptions.noGroup as option (option.value)}
                        {@const index = flattenedVisibleOptions.indexOf(option)}
                        {@render OptionItem({
                            option,
                            active: index === focusedIndex,
                            selected: option.value === value,
                            onclick: () => select(option),
                            onsemiactive: () => (focusedIndex = index),
                            "data-index": index,
                        })}
                    {/each}

                    {#each Object.entries(groupedOptions.groups) as [groupName, groupOptions] (groupName)}
                        <div
                            class="sticky top-0 z-10 mt-1 bg-surface-sunken/50 px-2 py-1.5 font-mono text-xs font-semibold tracking-wider text-on-surface-faint uppercase backdrop-blur-sm first:mt-0"
                        >
                            {groupName}
                        </div>
                        {#each groupOptions as option (option.value)}
                            {@const index = flattenedVisibleOptions.indexOf(option)}
                            {@render OptionItem({
                                option,
                                active: index === focusedIndex,
                                selected: option.value === value,
                                onclick: () => select(option),
                                onsemiactive: () => (focusedIndex = index),
                                "data-index": index,
                            })}
                        {/each}
                    {/each}
                </div>
            </div>
        </div>
    {/if}

    {#if error}
        <p class="mt-1.5 font-sans text-xs text-danger-soft-text">
            {error}
        </p>
    {:else if hint}
        <p class="mt-1.5 font-sans text-xs text-on-surface-muted">
            {hint}
        </p>
    {/if}
</div>

{#snippet OptionItem({
    option,
    active,
    selected,
    onclick,
    onsemiactive,
    ...rest
}: {
    option: SelectOption;
    active: boolean;
    selected: boolean;
    onclick: () => void;
    onsemiactive: () => void;
    [key: string]: unknown;
})}
    <button
        type="button"
        {onclick}
        onmouseenter={onsemiactive}
        class="
            flex w-full items-start gap-3 rounded-[3px] px-2.5 py-2 text-left {active
            ? 'bg-surface-sunken'
            : 'hover:bg-surface-sunken/50'}
            {selected ? 'bg-primary-soft/50 font-medium text-on-surface' : 'text-on-surface-muted'}
            {option.disabled
            ? 'pointer-events-none cursor-not-allowed opacity-50'
            : 'cursor-pointer'}
        "
        {...rest}
    >
        {#if option.icon}
            <div class="mt-0.5 shrink-0 {selected ? 'text-primary' : 'text-on-surface-faint'}">
                <option.icon size={16} />
            </div>
        {/if}

        <div class="min-w-0 flex-1">
            <div class="flex items-center justify-between gap-2">
                <span class="block truncate font-mono text-sm font-medium">{option.label}</span>
                {#if selected}
                    <Check size={14} class="shrink-0 text-primary" />
                {/if}
            </div>
            {#if option.description}
                <div
                    class="mt-0.5 line-clamp-2 font-sans text-xs leading-normal text-wrap text-on-surface-muted"
                >
                    {option.description}
                </div>
            {/if}
        </div>
    </button>
{/snippet}
