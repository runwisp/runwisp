// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later
// @ts-check

import { defineConfig } from "astro/config";
import starlight from "@astrojs/starlight";
import starlightOpenAPI, { openAPISidebarGroups } from "starlight-openapi";

export default defineConfig({
    site: "https://docs.runwisp.com",
    // Docs URLs are shipped inside released binaries, printed by the daemon,
    // embedded in scaffolded runwisp.toml files, and quoted in released
    // CHANGELOG entries — so a moved page keeps its old URL resolving forever.
    // The migration guides (cron, supervisord, docker-compose) were scattered
    // across two top-level sections and are now one "Coming from…" group; the
    // CLI and agent references moved into "Reference". `/configuration/scheduling`
    // never existed — a released CHANGELOG entry links it by mistake.
    // Every entry points at its final target: no redirect chains.
    redirects: {
        "/recipes/migrating-from-cron": "/coming-from/cron/",
        "/replacing-cron": "/coming-from/cron/",
        "/replacing-cron/take-over-from-cron": "/coming-from/cron/",
        "/replacing-cron/held-jobs": "/coming-from/cron/",
        "/replacing-cron/converting-crontabs": "/coming-from/crontabs/",
        "/replacing-cron/cron-mapping": "/coming-from/cron-mapping/",
        "/recipes/migrating-from-supervisord": "/coming-from/supervisord/",
        "/recipes/migrating-from-docker-compose": "/coming-from/docker-compose/",
        "/operations/cli": "/reference/cli/",
        "/operations/agents": "/reference/agents/",
        "/configuration/scheduling": "/concepts/scheduling/",
    },
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
            // theme-tokens.css @imports the webfonts it names, so the font
            // stack is declared in exactly one place for every consumer.
            customCss: ["@runwisp/ui/theme-tokens.css", "./src/styles/theme-bridge.css"],
            sidebar: [
                { label: "Welcome", link: "/" },
                {
                    label: "Getting Started",
                    items: [
                        { label: "Quick start", slug: "getting-started/quick-start" },
                        { label: "Docker", slug: "getting-started/docker" },
                        { label: "The Web UI tour", slug: "getting-started/web-ui-tour" },
                        { label: "The TUI tour", slug: "getting-started/tui-tour" },
                    ],
                },
                {
                    // Every migration route lives here, whatever the source. Cron
                    // used to own a top-level section while supervisord and
                    // docker-compose were filed under Recipes, so "I'm coming
                    // from X" had two different answers in two different places.
                    label: "Coming from…",
                    items: [
                        { label: "Start here", slug: "coming-from" },
                        { label: "From cron", slug: "coming-from/cron" },
                        { label: "Converting crontabs", slug: "coming-from/crontabs" },
                        { label: "How cron maps to TOML", slug: "coming-from/cron-mapping" },
                        { label: "From supervisord", slug: "coming-from/supervisord" },
                        { label: "From docker-compose", slug: "coming-from/docker-compose" },
                    ],
                },
                {
                    // Explanation, not lookup: these pages say why and when.
                    // Every key, default, and accepted value belongs to
                    // "Configuration Reference" and is not restated here.
                    label: "How it works",
                    items: [
                        { label: "Tasks vs Services", slug: "concepts/tasks-vs-services" },
                        { label: "How scheduling works", slug: "concepts/scheduling" },
                        { label: "Concurrency policies", slug: "concepts/concurrency" },
                        { label: "Retries & timeouts", slug: "concepts/retries" },
                        { label: "Parameters", slug: "concepts/parameters" },
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
                                    label: "Email (local MTA)",
                                    slug: "notifications/providers/sendmail",
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
                    // Worked examples only. The migration guides that used to sit
                    // here now live under "Coming from…".
                    label: "Guides",
                    items: [
                        { label: "Nightly backup", slug: "recipes/backup" },
                        { label: "Health checks", slug: "recipes/healthcheck" },
                        { label: "Deploy hooks", slug: "recipes/deploy-hooks" },
                        { label: "Trigger via API", slug: "recipes/remote-trigger" },
                        { label: "Docker patterns", slug: "recipes/docker" },
                    ],
                },
                {
                    label: "Running in Production",
                    items: [
                        { label: "Troubleshooting", slug: "operations/troubleshooting" },
                        { label: "Auth", slug: "operations/auth" },
                        { label: "Autostart", slug: "operations/autostart" },
                        { label: "Reload", slug: "operations/reload" },
                        { label: "Logging", slug: "operations/logging" },
                        { label: "Metrics", slug: "operations/metrics" },
                    ],
                },
                {
                    // The three lookup surfaces in one place. The CLI reference
                    // is a reference doc, not an operations task.
                    label: "Reference",
                    items: [
                        { label: "CLI reference", slug: "reference/cli" },
                        { label: "Driving with an AI agent", slug: "reference/agents" },
                    ],
                },
                ...openAPISidebarGroups,
            ],
        }),
    ],
});
