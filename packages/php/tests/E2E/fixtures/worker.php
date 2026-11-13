<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// The app WorkerTest drives: data dir, port and binary come from the env.

declare(strict_types=1);

require __DIR__ . '/../../../vendor/autoload.php';

use RunWisp\Run;
use RunWisp\RunWisp;

$config = [
    'defaults' => ['env' => ['REGION' => 'eu']],
    'tasks' => [
        'shell' => ['run' => 'echo from a shell task'],
        // Keeps RunWisp busy stopping for a while: it outlives SIGTERM.
        'linger' => ['run' => "trap '' TERM; sleep 2"],
    ],
];
// A deploy that changes [daemon], as WorkerTest makes one.
$daemon = getenv('RUNWISP_DATA') . '.daemon.json';
if (is_file($daemon)) {
    $config['daemon'] = json_decode((string) file_get_contents($daemon), true, flags: JSON_THROW_ON_ERROR);
}

$runwisp = new RunWisp([
    'auth' => false,
    'binary' => (string) getenv('RUNWISP_PHP_TEST_BINARY') ?: 'runwisp',
    'config' => $config,
]);

$runwisp->task('tick', ['cron' => '* * * * * *'], function (): void {
    echo "tick\n";
});
$runwisp->task('hello', ['retry_attempts' => 0], function (): void {
    // stdout and stderr are separate pipes, so only output a few ticks apart keeps its order.
    echo "hello from the app\n";
    usleep(400_000);
    fwrite(STDERR, "and a warning\n");
    usleep(400_000);
    echo 'no newline';
});
$runwisp->task('context', ['env' => ['TOKEN' => 't0ken'], 'params' => [['env' => 'DRY_RUN', 'default' => '1']]], function (Run $run): void {
    echo 'region=', getenv('REGION'), ' token=', $run->env['TOKEN'] ?? '', ' dry=', $run->params['DRY_RUN'] ?? '', "\n";
});
$runwisp->task('broken', [], function (): void {
    throw new RuntimeException('boom');
});
$runwisp->task('exits', [], fn (): int => 3);
$runwisp->task('skips', [], fn (Run $run): int => $run->skip('nothing to do'));
$runwisp->service('worker', [], function (Run $run): void {
    echo "worker up\n";
    while (!$run->stopping()) {
        usleep(50_000);
    }
    echo "worker stopping\n";
});

// A deploy, as WorkerTest makes one: a reload reads the tasks again from this
// file, so what is here now is what it finds.
$deploy = getenv('RUNWISP_DATA') . '.deploy.php';
if (is_file($deploy)) {
    require $deploy;
}

exit($runwisp->main());
