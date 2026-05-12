// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare module "*.svelte" {
    import type { Component } from "svelte";
    const component: Component;
    export default component;
}

declare module "*.css?inline" {
    const css: string;
    export default css;
}

declare module "*.svg?raw" {
    const svg: string;
    export default svg;
}
