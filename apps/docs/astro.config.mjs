// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0
// @ts-check

import { defineConfig } from "astro/config";
import starlight from "@astrojs/starlight";
import starlightOpenAPI, { openAPISidebarGroups } from "starlight-openapi";

export default defineConfig({
    site: "https://docs.runwisp.com",
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
                src: "@runwisp/ui/assets/runwisp-logo.svg",
            },
            favicon: "/favicon.svg",
            head: [
                {
                    tag: "link",
                    attrs: { rel: "icon", href: "/favicon.ico", sizes: "any" },
                },
                {
                    tag: "link",
                    attrs: { rel: "apple-touch-icon", href: "/apple-touch-icon.png" },
                },
                {
                    tag: "link",
                    attrs: { rel: "manifest", href: "/site.webmanifest" },
                },
                {
                    tag: "meta",
                    attrs: { name: "theme-color", content: "#15a0a8" },
                },
            ],
            social: [
                {
                    icon: "external",
                    label: "Website",
                    href: "https://runwisp.com",
                },
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
                "@runwisp/ui/theme-tokens.css",
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
                        { label: "Quick start", slug: "getting-started/quick-start" },
                        { label: "The Web UI tour", slug: "getting-started/web-ui-tour" },
                        { label: "The TUI tour", slug: "getting-started/tui-tour" },
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
                    ],
                },
                {
                    label: "Configuration Reference",
                    items: [
                        { label: "Overview", slug: "configuration/overview" },
                        { label: "[storage]", slug: "configuration/storage" },
                        { label: "[daemon]", slug: "configuration/daemon" },
                        { label: "[defaults]", slug: "configuration/defaults" },
                        { label: "[tasks.*]", slug: "configuration/tasks" },
                        { label: "[services.*]", slug: "configuration/services" },
                        { label: "[compose.*]", slug: "configuration/compose" },
                        { label: "${...} substitution", slug: "configuration/substitution" },
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
                                    label: "Discord",
                                    slug: "notifications/providers/discord",
                                },
                                {
                                    label: "Telegram",
                                    slug: "notifications/providers/telegram",
                                },
                                {
                                    label: "Email (SMTP)",
                                    slug: "notifications/providers/smtp",
                                },
                                {
                                    label: "Webhook",
                                    slug: "notifications/providers/webhook",
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
                        {
                            label: "Migrating from cron",
                            slug: "recipes/migrating-from-cron",
                        },
                        {
                            label: "Migrating from supervisord",
                            slug: "recipes/migrating-from-supervisord",
                        },
                        { label: "Nightly backup", slug: "recipes/backup" },
                        { label: "Health checks", slug: "recipes/healthcheck" },
                        { label: "Deploy hooks", slug: "recipes/deploy-hooks" },
                        {
                            label: "Trigger via API",
                            slug: "recipes/remote-trigger",
                        },
                        { label: "Docker patterns", slug: "recipes/docker" },
                        {
                            label: "Migrating from docker-compose",
                            slug: "recipes/migrating-from-docker-compose",
                        },
                    ],
                },
                {
                    label: "Operations",
                    items: [
                        { label: "CLI reference", slug: "operations/cli" },
                        { label: "Driving with an AI agent", slug: "operations/agents" },
                        { label: "Auth", slug: "operations/auth" },
                        { label: "Autostart", slug: "operations/autostart" },
                        { label: "Reload", slug: "operations/reload" },
                        { label: "Logging", slug: "operations/logging" },
                        { label: "Metrics", slug: "operations/metrics" },
                    ],
                },
                ...openAPISidebarGroups,
            ],
        }),
    ],
});
