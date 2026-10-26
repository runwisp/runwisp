<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// The app WorkerTest drives: data dir, port and binary come from the env.

declare(strict_types=1);

require __DIR__ . '/../../../vendor/autoload.php';

use RunWisp\Run;
use RunWisp\RunWisp;

$runwisp = new RunWisp([
    'auth' => false,
    'binary' => (string) getenv('RUNWISP_PHP_TEST_BINARY') ?: 'runwisp',
    'config' => [
        'defaults' => ['env' => ['REGION' => 'eu']],
        'tasks' => [
            'shell' => ['run' => 'echo from a shell task'],
        ],
    ],
]);

$runwisp->task('tick', ['cron' => '* * * * * *'], function (): void {
    echo "tick\n";
});
$runwisp->task('hello', ['retry_attempts' => 0], function (): void {
    echo "hello from the app\n";
    fwrite(STDERR, "and a warning\n");
    echo 'no newline';
});
$runwisp->task('context', ['env' => ['TOKEN' => 't0ken'], 'params' => [['env' => 'DRY_RUN', 'default' => '1']]], function (Run $run): void {
    echo 'region=', getenv('REGION'), ' token=', $run->env['TOKEN'] ?? '', ' dry=', $run->params['DRY_RUN'] ?? '', "\n";
});
$runwisp->task('broken', [], function (): void {
    throw new RuntimeException('boom');
});
$runwisp->task('exits', [], fn (): int => 3);
$runwisp->service('worker', [], function (Run $run): void {
    echo "worker up\n";
    while (!$run->stopping()) {
        usleep(50_000);
    }
    echo "worker stopping\n";
});

exit($runwisp->main());
