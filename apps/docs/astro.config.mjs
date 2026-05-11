// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0
// @ts-check

import { defineConfig } from "astro/config";
import starlight from "@astrojs/starlight";
import starlightOpenAPI, { openAPISidebarGroups } from "starlight-openapi";

export default defineConfig({
    integrations: [
        starlight({
            plugins: [
                starlightOpenAPI([
                    {
                        base: "api",
                        schema: "./public/openapi.json",
                        sidebar: { label: "API Reference" },
                    },
                ]),
            ],
            title: "RunWisp",
            logo: {
                src: "./src/assets/runwisp-logo.svg",
            },
            social: [
                {
                    icon: "github",
                    label: "GitHub",
                    href: "https://github.com/runwisp/runwisp",
                },
            ],
            editLink: {
                baseUrl: "https://github.com/runwisp/runwisp/edit/main/apps/docs/",
            },
            customCss: [
                "./src/styles/runwisp-tokens.css",
                "./src/styles/theme-bridge.css",
                "@fontsource/tasa-orbiter/400.css",
                "@fontsource/tasa-orbiter/500.css",
                "@fontsource/tasa-orbiter/600.css",
                "@fontsource/tasa-orbiter/700.css",
                "@fontsource/jetbrains-mono/400.css",
                "@fontsource/jetbrains-mono/500.css",
                "@fontsource/jetbrains-mono/700.css",
            ],
            sidebar: [
                { label: "Welcome", link: "/" },
                {
                    label: "Getting Started",
                    items: [
                        { label: "Install", slug: "getting-started/install" },
                        { label: "Your first task", slug: "getting-started/first-task" },
                        { label: "The Web UI tour", slug: "getting-started/web-ui-tour" },
                    ],
                },
                {
                    label: "Concepts",
                    items: [
                        { label: "Tasks vs Services", slug: "concepts/tasks-vs-services" },
                        { label: "How scheduling works", slug: "concepts/scheduling" },
                        { label: "Concurrency policies", slug: "concepts/concurrency" },
                        { label: "Retries & timeouts", slug: "concepts/retries" },
                        { label: "Logs & retention", slug: "concepts/logs" },
                        { label: "The TUI tour", slug: "concepts/tui-tour" },
                    ],
                },
                {
                    label: "Configuration Reference",
                    items: [
                        { label: "Overview", slug: "configuration/overview" },
                        { label: "[storage]", slug: "configuration/storage" },
                        { label: "[defaults]", slug: "configuration/defaults" },
                        { label: "[tasks.*]", slug: "configuration/tasks" },
                        { label: "[services.*]", slug: "configuration/services" },
                        { label: "Validation rules", slug: "configuration/validation" },
                    ],
                },
                {
                    label: "Notifications",
                    items: [
                        { label: "Model", slug: "notifications/model" },
                        {
                            label: "Providers",
                            items: [
                                { label: "Slack", slug: "notifications/providers/slack" },
                                {
                                    label: "Telegram",
                                    slug: "notifications/providers/telegram",
                                },
                            ],
                        },
                        { label: "Per-task notifications", slug: "notifications/per-task" },
                        { label: "Notification rules", slug: "notifications/routes" },
                        { label: "Global settings", slug: "notifications/global" },
                    ],
                },
                {
                    label: "Recipes",
                    items: [
                        { label: "Nightly backup", slug: "recipes/backup" },
                        { label: "Health checks", slug: "recipes/healthcheck" },
                        { label: "Deploy hooks", slug: "recipes/deploy-hooks" },
                        { label: "Docker patterns", slug: "recipes/docker" },
                    ],
                },
                { label: "Auth", slug: "operations/auth" },
                // ...openAPISidebarGroups,
            ],
        }),
    ],
});
