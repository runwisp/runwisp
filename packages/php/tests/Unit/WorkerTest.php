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

    public function testTheDashboardNoticeSaysWhenItListensOnAllInterfaces(): void
    {
        self::assertSame(
            ['info: RunWisp dashboard: http://127.0.0.1:9477 (listening on all interfaces) (login off)'],
            self::announced(self::settings('0.0.0.0', false)),
        );
        self::assertSame(
            ['info: RunWisp dashboard: http://[::1]:9477 (login off)'],
            self::announced(self::settings('::1', false)),
        );
    }

    /** @return list<string> */
    private static function announced(Settings $settings): array
    {
        $lines = [];
        $worker = new Worker($settings, [], [], static function (string $level, string $message) use (&$lines): void {
            $lines[] = "$level: $message";
        });
        (new \ReflectionMethod($worker, 'announce'))->invoke($worker);

        return $lines;
    }

    private static function settings(string $host = '127.0.0.1', bool $auth = true): Settings
    {
        return new Settings('app', '/tmp/rw-unit', 9477, $host, null, $auth, 'runwisp', '/tmp');
    }
}
