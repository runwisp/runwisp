// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later
// @ts-check

import { defineConfig } from "astro/config";
import starlight from "@astrojs/starlight";
import svelte from "@astrojs/svelte";
import starlightOpenAPI, { openAPISidebarGroups } from "starlight-openapi";
import { redirects } from "./src/redirects.mjs";

// Code blocks render as the marketing site's always-dark operator terminal in
// both light and dark mode. Surface + syntax colours are lifted verbatim from
// runwisp.com's --term / --syn-* tokens (src/styles/app.css) so a code sample
// looks the same on docs.runwisp.com as it does on the landing page.
const terminalTheme = {
    name: "runwisp-terminal",
    type: "dark",
    colors: {
        "editor.background": "#0a1116",
        "editor.foreground": "#dbe6ec",
    },
    tokenColors: [
        {
            scope: ["comment", "punctuation.definition.comment"],
            settings: { foreground: "#5b6b75", fontStyle: "italic" },
        },
        {
            scope: ["keyword", "storage", "storage.type", "keyword.control", "keyword.operator"],
            settings: { foreground: "#79c7ff" },
        },
        {
            scope: ["string", "string.quoted", "punctuation.definition.string"],
            settings: { foreground: "#7fd6a0" },
        },
        {
            scope: ["constant.numeric", "constant.language", "constant"],
            settings: { foreground: "#e0a52e" },
        },
        {
            scope: ["entity.name.function", "support.function", "meta.function-call"],
            settings: { foreground: "#2fd4dd" },
        },
        {
            scope: ["punctuation", "meta.brace", "punctuation.separator", "punctuation.terminator"],
            settings: { foreground: "#9aa7b0" },
        },
        {
            scope: ["variable", "meta.definition.variable"],
            settings: { foreground: "#dbe6ec" },
        },
    ],
};

export default defineConfig({
    site: "https://docs.runwisp.com",
    redirects,
    integrations: [
        svelte(),
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
            // One fixed dark terminal theme for both colour modes. Frame radius +
            // hairline match the flat 3px chrome; the frame's drop shadow is
            // suppressed here because theme-bridge.css casts the deeper --lift-3.
            expressiveCode: {
                themes: [terminalTheme],
                useStarlightDarkModeSwitch: false,
                styleOverrides: {
                    borderRadius: "3px",
                    borderColor: "var(--rw-outline)",
                    frames: {
                        frameBoxShadowCssValue: "none",
                    },
                },
            },
            title: "RunWisp",
            logo: {
                src: "@runwisp/ui/assets/runwisp-logo.svg",
            },
            favicon: "/favicon.svg",
            components: {
                // Adds a one-line HTML comment pointing AI agents at the
                // Markdown twin of the page they just fetched as HTML.
                Head: "./src/components/Head.astro",
                SocialIcons: "./src/components/SocialIcons.astro",
            },
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
                    attrs: {
                        name: "theme-color",
                        media: "(prefers-color-scheme: light)",
                        content: "#f5f8f9",
                    },
                },
                {
                    tag: "meta",
                    attrs: {
                        name: "theme-color",
                        media: "(prefers-color-scheme: dark)",
                        content: "#0c1719",
                    },
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
            // Symfony-style layout: Getting Started (install, first run), Guides
            // (one page per feature, explanation + examples), Reference (every
            // key and command, lookup only). A fact lives on one page; the rest
            // link to it. Merge into an existing page before adding one.
            sidebar: [
                { label: "Welcome", link: "/" },
                {
                    label: "Getting Started",
                    items: [
                        { label: "Quick start", slug: "getting-started/quick-start" },
                        { label: "Running in Docker", slug: "getting-started/docker" },
                        { label: "Web UI", slug: "getting-started/web-ui-tour" },
                        { label: "TUI", slug: "getting-started/tui-tour" },
                    ],
                },
                {
                    label: "Guides",
                    items: [
                        { label: "Tasks and services", slug: "concepts/tasks-vs-services" },
                        { label: "Scheduling", slug: "concepts/scheduling" },
                        { label: "Overlapping runs", slug: "concepts/concurrency" },
                        { label: "Failures, retries & timeouts", slug: "concepts/retries" },
                        { label: "Parameters", slug: "concepts/parameters" },
                        { label: "Run logs", slug: "concepts/logs" },
                        {
                            label: "Notifications",
                            items: [
                                { label: "Overview", slug: "notifications" },
                                { label: "Slack", slug: "notifications/providers/slack" },
                                { label: "Discord", slug: "notifications/providers/discord" },
                                { label: "Telegram", slug: "notifications/providers/telegram" },
                                { label: "Email (SMTP)", slug: "notifications/providers/smtp" },
                                {
                                    label: "Email (local MTA)",
                                    slug: "notifications/providers/sendmail",
                                },
                                { label: "Webhook", slug: "notifications/providers/webhook" },
                            ],
                        },
                        { label: "Docker tasks", slug: "recipes/docker" },
                        { label: "Remote triggers", slug: "recipes/remote-trigger" },
                    ],
                },
                {
                    label: "Examples",
                    items: [
                        { label: "Nightly backup", slug: "recipes/backup" },
                        { label: "Health checks", slug: "recipes/healthcheck" },
                    ],
                },
                {
                    label: "Migrating",
                    items: [
                        { label: "Overview", slug: "coming-from" },
                        { label: "From cron", slug: "coming-from/cron" },
                        { label: "From supervisord", slug: "coming-from/supervisord" },
                        { label: "From systemd", slug: "coming-from/systemd" },
                        { label: "From docker-compose", slug: "coming-from/docker-compose" },
                    ],
                },
                {
                    label: "Operations",
                    items: [
                        { label: "Autostart", slug: "operations/autostart" },
                        { label: "Reload & restart", slug: "operations/reload" },
                        { label: "Authentication", slug: "operations/auth" },
                        { label: "Daemon log", slug: "operations/logging" },
                        { label: "Metrics", slug: "operations/metrics" },
                        { label: "Troubleshooting", slug: "operations/troubleshooting" },
                    ],
                },
                {
                    label: "Reference",
                    items: [
                        {
                            label: "Configuration",
                            items: [
                                { label: "Overview", slug: "configuration/overview" },
                                { label: "[tasks.*]", slug: "configuration/tasks" },
                                { label: "[services.*]", slug: "configuration/services" },
                                { label: "[compose.*]", slug: "configuration/compose" },
                                { label: "[defaults]", slug: "configuration/defaults" },
                                { label: "[daemon]", slug: "configuration/daemon" },
                                { label: "[storage]", slug: "configuration/storage" },
                                { label: "[notify]", slug: "configuration/notify" },
                                { label: "[[route]]", slug: "configuration/routes" },
                                {
                                    label: "${...} substitution",
                                    slug: "configuration/substitution",
                                },
                            ],
                        },
                        { label: "CLI", slug: "reference/cli" },
                        { label: "AI agents", slug: "reference/agents" },
                    ],
                },
                ...openAPISidebarGroups,
            ],
        }),
    ],
});
