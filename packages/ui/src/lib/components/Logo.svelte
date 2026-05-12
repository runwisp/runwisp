<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import logoSvg from "../assets/runwisp-logo.svg?raw";

    interface Props {
        size?: "xs" | "sm" | "md" | "lg" | "xl";
        class?: string;
    }

    let { size = "md", class: className = "" }: Props = $props();

    const sizeClasses = {
        xs: "w-4 h-4",
        sm: "w-5 h-5",
        md: "w-6 h-6",
        lg: "w-8 h-8",
        xl: "w-12 h-12",
    };
</script>

<span class="inline-flex {sizeClasses[size]} {className}" aria-hidden="true">
    <!-- SVG body is a static, repo-vendored asset imported via Vite's ?raw loader. -->
    <!-- eslint-disable-next-line svelte/no-at-html-tags -->
    {@html logoSvg}
</span>

<style>
    span :global(svg) {
        width: 100%;
        height: 100%;
    }
</style>
