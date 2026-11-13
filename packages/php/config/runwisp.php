<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

// RunWisp for `php artisan runwisp:work`. A null key falls back to RunWisp's
// default. The env() calls are read when Laravel loads its config, so they
// keep working after `php artisan config:cache`.
return [
    // Which app this is: copies of one app share a RunWisp. Default: the
    // `name` in composer.json.
    'app' => env('RUNWISP_APP'),

    // Database, run logs and socket. Default: storage/runwisp.
    'data' => env('RUNWISP_DATA'),

    // Web UI port. Default: 9477 or the next free port.
    'port' => env('RUNWISP_PORT'),

    // Web UI bind address. Default: 127.0.0.1.
    'host' => env('RUNWISP_HOST'),

    // Web UI password. Default: a new one on each start.
    'password' => env('RUNWISP_PASSWORD'),

    // false turns the login off. Default: on, off with RUNWISP_AUTH=off.
    'auth' => env('RUNWISP_AUTH') === 'off' ? false : null,

    // Path to the runwisp binary. Default: the one Composer downloaded, else
    // runwisp on PATH.
    'binary' => env('RUNWISP_BINARY'),

    // The rest of a runwisp.toml, with the same keys: notifiers, route,
    // defaults, daemon, storage, services, and shell tasks. A scheduled task's
    // own keys, such as retry_attempts, go in #[RunWisp\Task] on its command
    // or job.
    'config' => [
        // A queue worker that RunWisp keeps running and restarts:
        // 'services' => [
        //     'queue-worker' => [
        //         'run' => 'php artisan queue:work --sleep=3 --tries=3 --max-time=3600',
        //         'restart_delay' => '5s',
        //     ],
        // ],
    ],
];
