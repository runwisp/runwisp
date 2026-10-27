<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\Tests\Unit;

use PHPUnit\Framework\TestCase;
use RunWisp\Settings;
use RunWisp\Unit;
use RunWisp\Worker;

final class WorkerTest extends TestCase
{
    public function testDocumentMarksCodeUnitsSdkNextToTheConfigsTasks(): void
    {
        $worker = new Worker(self::settings(), [
            'defaults' => ['keep_runs' => 10],
            'tasks' => ['vacuum' => ['run' => 'echo vacuum']],
        ], [
            new Unit('backup', ['cron' => '0 3 * * *', 'timeout' => '1h'], ['php', 'x']),
            new Unit('queue', ['restart_delay' => '5s'], ['php', 'x'], true),
        ]);

        self::assertSame([
            'defaults' => ['keep_runs' => 10],
            'tasks' => [
                'vacuum' => ['run' => 'echo vacuum'],
                'backup' => ['cron' => '0 3 * * *', 'timeout' => '1h', 'sdk' => true],
            ],
            'services' => ['queue' => ['restart_delay' => '5s', 'sdk' => true]],
        ], json_decode($worker->document(), true));
    }

    public function testATaskInBothTheConfigAndTheCodeIsAnError(): void
    {
        $worker = new Worker(self::settings(), ['tasks' => ['backup' => ['retry_attempts' => 3]]], [
            new Unit('backup', ['cron' => '0 3 * * *'], ['php', 'x']),
        ]);

        $this->expectExceptionMessage('the config\'s tasks also define "backup", which the code defines');
        $worker->document();
    }

    public function testEmptyUnitMapsAreObjects(): void
    {
        self::assertSame('{"tasks":{},"services":{}}', (new Worker(self::settings(), [], []))->document());
    }

    private static function settings(): Settings
    {
        return new Settings('app', '/tmp/rw-unit', null, '127.0.0.1', null, true, 'runwisp', '/tmp');
    }
}
