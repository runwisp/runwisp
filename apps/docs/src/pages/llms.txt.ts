// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Curated llms.txt index (https://llmstxt.org). Section order is hand-defined
// here; per-page titles/descriptions are pulled from frontmatter so the index
// never drifts as pages change. Every link points at the raw `.md` form served
// by `[...slug].md.ts`, so an agent can fetch any page as plain text.

import type { APIRoute } from "astro";
import { getCollection } from "astro:content";

const SITE = "https://docs.runwisp.com";

const SUMMARY =
    "One small Go binary that replaces crond + supervisord: it schedules tasks and " +
    "supervises long-running services, and persists every run's exit code, duration, " +
    "timestamps, and captured stdout/stderr — browsable and streamable from an embedded " +
    "Web UI, a TUI, and a REST API. Local-first, offline-complete, zero runtime deps. " +
    "Every task is defined in a single runwisp.toml; the API and UI are read-only + trigger.";

// Ordered sections of human docs. Slugs are content-collection ids.
const SECTIONS: ReadonlyArray<{ label: string; slugs: ReadonlyArray<string> }> = [
    { label: "Overview", slugs: ["index"] },
    {
        label: "Getting started",
        slugs: [
            "getting-started/quick-start",
            "getting-started/web-ui-tour",
            "getting-started/tui-tour",
        ],
    },
    {
        label: "Concepts",
        slugs: [
            "concepts/tasks-vs-services",
            "concepts/scheduling",
            "concepts/concurrency",
            "concepts/retries",
            "concepts/logs",
        ],
    },
    {
        label: "Configuration",
        slugs: [
            "configuration/overview",
            "configuration/storage",
            "configuration/daemon",
            "configuration/defaults",
            "configuration/tasks",
            "configuration/services",
            "configuration/compose",
            "configuration/substitution",
        ],
    },
    {
        label: "Notifications",
        slugs: [
            "notifications/model",
            "notifications/per-task",
            "notifications/routes",
            "notifications/global",
            "notifications/providers/slack",
            "notifications/providers/telegram",
            "notifications/providers/smtp",
        ],
    },
    {
        label: "Operations",
        slugs: [
            "operations/auth",
            "operations/autostart",
            "operations/logging",
            "operations/metrics",
        ],
    },
    {
        label: "Recipes",
        slugs: [
            "recipes/backup",
            "recipes/healthcheck",
            "recipes/deploy-hooks",
            "recipes/docker",
            "recipes/migrating-from-docker-compose",
        ],
    },
];

export const GET: APIRoute = async () => {
    const docs = await getCollection("docs");
    const byId = new Map(docs.map((entry) => [entry.id, entry]));

    const lines: string[] = ["# RunWisp", "", `> ${SUMMARY}`, ""];

    lines.push("## Agent reference", "");
    lines.push(
        `- [RunWisp agent reference](${SITE}/agents/reference.md): Dense, ` +
            "token-optimized full reference — complete runwisp.toml schema, CLI, and REST " +
            "surface, written for agents. Start here.",
        "",
    );

    for (const section of SECTIONS) {
        lines.push(`## ${section.label}`, "");
        for (const slug of section.slugs) {
            const entry = byId.get(slug);
            if (!entry) {
                throw new Error(`llms.txt references unknown docs page: ${slug}`);
            }
            const description = entry.data.description ?? "";
            lines.push(`- [${entry.data.title}](${SITE}/${slug}.md): ${description}`);
        }
        lines.push("");
    }

    lines.push("## Optional", "");
    lines.push(
        `- [OpenAPI schema](${SITE}/openapi.json): Full REST API specification (OpenAPI 3.1, JSON).`,
        "",
    );

    // Lead with a UTF-8 BOM so browsers decode as UTF-8 even when the static
    // host serves text/plain without a charset (+ `nosniff`). See [...slug].md.ts.
    return new Response("\uFEFF" + lines.join("\n"), {
        headers: { "Content-Type": "text/plain; charset=utf-8" },
    });
};
