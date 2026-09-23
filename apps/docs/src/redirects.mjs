// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Docs URLs are shipped inside released binaries, printed by the daemon,
// embedded in scaffolded runwisp.toml files, and quoted in released
// CHANGELOG entries — so a moved page keeps its old URL resolving forever.
// The migration guides (cron, supervisord, docker-compose) were scattered
// across two top-level sections and are now one "Coming from…" group; the
// CLI and agent references moved into "Reference". `/configuration/scheduling`
// never existed — a released CHANGELOG entry links it by mistake. The
// Symfony-style rewrite merged the three cron pages into one, deploy hooks into
// remote triggers, the notification model and per-task pages into one guide,
// and moved [notify] and [[route]] into the configuration reference.
// Every entry points at its final target: no redirect chains.
//
// Shared between astro.config.mjs (browser redirects) and
// src/pages/[...slug].md.ts (markdown stubs at the same old paths, so an
// agent asking for Accept: text/markdown at an old URL gets pointed at the
// new one instead of a 404).
export const redirects = {
    "/recipes/migrating-from-cron": "/coming-from/cron/",
    "/replacing-cron": "/coming-from/cron/",
    "/replacing-cron/take-over-from-cron": "/coming-from/cron/",
    "/replacing-cron/held-jobs": "/coming-from/cron/",
    "/replacing-cron/converting-crontabs": "/coming-from/cron/",
    "/replacing-cron/cron-mapping": "/coming-from/cron/",
    "/recipes/migrating-from-supervisord": "/coming-from/supervisord/",
    "/recipes/migrating-from-docker-compose": "/coming-from/docker-compose/",
    "/operations/cli": "/reference/cli/",
    "/operations/agents": "/reference/agents/",
    "/configuration/scheduling": "/concepts/scheduling/",
    "/coming-from/crontabs": "/coming-from/cron/",
    "/coming-from/cron-mapping": "/coming-from/cron/",
    "/recipes/deploy-hooks": "/recipes/remote-trigger/",
    "/notifications/model": "/notifications/",
    "/notifications/per-task": "/notifications/",
    "/notifications/global": "/configuration/notify/",
    "/notifications/routes": "/configuration/routes/",
};
