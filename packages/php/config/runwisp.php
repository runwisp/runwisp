<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

// RunWisp for `php artisan runwisp:work`. A null key falls back to the
// RUNWISP_* env var the runwisp CLI reads, then to RunWisp's default.
return [
    // Which app this is: copies of one app share a RunWisp. Default: the
    // `name` in composer.json.
    'app' => null,

    // Database, run logs and socket. Default: RUNWISP_DATA, else storage/runwisp.
    'data' => null,

    // Web UI port. Default: RUNWISP_PORT, else 9477 or the next free port.
    'port' => null,

    // Web UI bind address. Default: RUNWISP_HOST, else 127.0.0.1.
    'host' => null,

    // Web UI password. Default: RUNWISP_PASSWORD, else a new one on each start.
    'password' => null,

    // false turns the login off, like RUNWISP_AUTH=off.
    'auth' => null,

    // Path to the runwisp binary. Default: RUNWISP_BINARY, else the one
    // Composer downloaded, else runwisp on PATH.
    'binary' => null,

    // The rest of a runwisp.toml, with the same keys: notifiers, route,
    // defaults, daemon, storage, and shell tasks. A scheduled task's own keys,
    // such as retry_attempts, go in #[RunWisp\Task] on its command or job.
    'config' => [],
];
