// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

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

// Ordered sections of human docs. Mirrors the site sidebar in astro.config.mjs.
// Every page in the collection must appear in exactly one section — the build
// throws otherwise (see the coverage check in GET), so this index cannot drift
// as pages are added or moved.
const SECTIONS: ReadonlyArray<{ label: string; slugs: ReadonlyArray<string> }> = [
    { label: "Overview", slugs: ["index"] },
    {
        label: "Getting started",
        slugs: [
            "getting-started/quick-start",
            "getting-started/docker",
            "getting-started/web-ui-tour",
            "getting-started/tui-tour",
        ],
    },
    {
        label: "Coming from cron, supervisord, or docker-compose",
        slugs: [
            "coming-from",
            "coming-from/cron",
            "coming-from/crontabs",
            "coming-from/cron-mapping",
            "coming-from/supervisord",
            "coming-from/docker-compose",
        ],
    },
    {
        label: "How it works",
        slugs: [
            "concepts/tasks-vs-services",
            "concepts/scheduling",
            "concepts/concurrency",
            "concepts/retries",
            "concepts/parameters",
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
            "notifications/providers/discord",
            "notifications/providers/telegram",
            "notifications/providers/smtp",
            "notifications/providers/sendmail",
            "notifications/providers/webhook",
        ],
    },
    {
        label: "Guides",
        slugs: [
            "recipes/backup",
            "recipes/healthcheck",
            "recipes/deploy-hooks",
            "recipes/remote-trigger",
            "recipes/docker",
        ],
    },
    {
        label: "Running in production",
        slugs: [
            "operations/troubleshooting",
            "operations/auth",
            "operations/autostart",
            "operations/reload",
            "operations/logging",
            "operations/metrics",
        ],
    },
    { label: "Reference", slugs: ["reference/cli", "reference/agents"] },
];

export const GET: APIRoute = async () => {
    const docs = await getCollection("docs");
    const byId = new Map(docs.map((entry) => [entry.id, entry]));

    // Fail the build on a page this index forgot. Without this the omission is
    // silent: the page simply never reaches an agent reading /llms.txt, which
    // is how the whole cron-migration section and the CLI reference went
    // missing from it once already.
    const listed = new Set(SECTIONS.flatMap((section) => section.slugs));
    const unlisted = docs.map((entry) => entry.id).filter((id) => !listed.has(id));
    if (unlisted.length > 0) {
        throw new Error(
            `llms.txt is missing ${String(unlisted.length)} docs page(s): ${unlisted.sort().join(", ")}. ` +
                "Add them to SECTIONS in src/pages/llms.txt.ts.",
        );
    }

    const lines: string[] = ["# RunWisp", "", `> ${SUMMARY}`, ""];

    lines.push(
        "## Agent reference",
        "",
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

    lines.push(
        "## Optional",
        "",
        `- [runwisp.toml JSON Schema](${SITE}/config.schema.json): Machine-readable schema (draft 2020-12) for the config file; also \`runwisp schema\`. Editors read it via a \`#:schema\` directive.`,
        `- [OpenAPI schema](${SITE}/openapi.json): Full REST API specification (OpenAPI 3.1, JSON).`,
        "",
    );

    // Lead with a UTF-8 BOM so browsers decode as UTF-8 even when the static
    // host serves text/plain without a charset (+ `nosniff`). See [...slug].md.ts.
    return new Response("\uFEFF" + lines.join("\n"), {
        headers: { "Content-Type": "text/plain; charset=utf-8" },
    });
};
