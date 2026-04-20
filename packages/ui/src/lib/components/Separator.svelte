<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    type SeparatorOrientation = "horizontal" | "vertical";

    interface Props {
        orientation?: SeparatorOrientation;
        label?: string;
        class?: string;
    }

    let { orientation = "horizontal", label, class: className = "" }: Props = $props();
</script>

{#if orientation === "vertical"}
    <div
        role="separator"
        aria-orientation="vertical"
        class="inline-block w-px self-stretch bg-outline {className}"
    ></div>
{:else if label}
    <div role="separator" class="flex items-center gap-3 {className}">
        <div class="h-px flex-1 bg-outline"></div>
        <span class="text-xs font-medium text-on-surface-faint select-none">{label}</span>
        <div class="h-px flex-1 bg-outline"></div>
    </div>
{:else}
    <hr class="border-outline {className}" />
{/if}
