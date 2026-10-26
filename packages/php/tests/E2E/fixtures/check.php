<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// The app CheckTest checks; RUNWISP_TEST_TYPO misspells a task option.

declare(strict_types=1);

require __DIR__ . '/../../../vendor/autoload.php';

use RunWisp\RunWisp;

$runwisp = new RunWisp([
    'binary' => (string) getenv('RUNWISP_PHP_TEST_BINARY') ?: 'runwisp',
]);

$runwisp->task('nightly-backup', ['cron' => '0 3 * * *', 'timezone' => 'UTC', getenv('RUNWISP_TEST_TYPO') ? 'retry_attemps' : 'retry_attempts' => 3], fn () => null);
$runwisp->task('by-hand', [], function (): void {
    echo "ran by hand\n";
});
$runwisp->service('queue', ['restart_delay' => '5s'], fn () => null);

exit($runwisp->main());
