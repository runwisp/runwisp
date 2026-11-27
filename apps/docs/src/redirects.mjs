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
// and moved [notify] and [[route]] into the configuration reference. The
// install page became the quick start and the TUI tour moved into Getting started.
// API reference pages follow renamed OpenAPI operationIds to their current operation.
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
    "/getting-started/install": "/getting-started/quick-start/",
    "/concepts/tui-tour": "/getting-started/tui-tour/",
    "/api/operations/triggerrun": "/api/operations/runtask/",
    "/api/operations/restartservice": "/api/operations/restarttask/",
    "/api/operations/stopservice": "/api/operations/stoptask/",
    "/api/operations/getrunbyid": "/api/operations/getrun/",
    "/api/operations/getallruns": "/api/operations/listruns/",
    "/api/operations/gettasks": "/api/operations/listtasks/",
    "/api/operations/listtaskruns": "/api/operations/listruns/",
    "/api/operations/gettaskruns": "/api/operations/listruns/",
    "/api/operations/bulkcancelruns": "/api/operations/bulkstopruns/",
    "/api/operations/getinfo": "/api/operations/getdaemoninfo/",
    "/api/operations/getinstance": "/api/operations/getdaemonidentity/",
    "/api/operations/streamnotifications": "/api/operations/streamappevents/",
    "/api/operations/streamruns": "/api/operations/streamappevents/",
};
