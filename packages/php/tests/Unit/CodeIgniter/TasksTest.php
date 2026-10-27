<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\Tests\Unit\CodeIgniter;

use CodeIgniter\Tasks\Scheduler;
use PHPUnit\Framework\TestCase;
use RunWisp\CodeIgniter\Tasks;
use RunWisp\Task;

final class TasksTest extends TestCase
{
    public function testMapsTheSchedulerToTasks(): void
    {
        $scheduler = new Scheduler();
        $scheduler->command('reports:send')->daily('3:00 am');
        $scheduler->shell('cp foo bar')->hourly();
        $scheduler->call(static fn () => 1)->everyFiveMinutes()->named('cleanup')->singleInstance();
        $scheduler->call(static fn () => 2)->mondays();
        $scheduler->call(#[Task('prune', retry_attempts: 2)] static fn () => 3)->daily();
        $scheduler->command('digest:send')->daily();
        $scheduler->event('nightly')->daily();
        $scheduler->url('https://example.com/ping')->everyMinute();
        $scheduler->queue('default', 'mail', [])->hourly();
        $scheduler->command('reports:send')->hourly();
        $scheduler->command('cache:warm')->hourly()->environments('staging');

        $warnings = [];
        $units = Tasks::units($scheduler, 'production', 'Europe/Bratislava', '/app/spark', ['digest:send' => SendDigest::class], static function (string $m) use (&$warnings): void {
            $warnings[] = $m;
        });

        $line = __LINE__ - 14;
        self::assertSame([
            'reports:send',
            'cp-foo-bar',
            'cleanup',
            "closure:TasksTest.php:$line",
            'prune',
            'digest',
            'nightly',
            'https:-example.com-ping',
            'queue:default:mail',
            'reports:send-2',
        ], array_keys($units));

        [$unit] = $units['reports:send'];
        self::assertSame(['cron' => '00 3 * * *', 'timezone' => 'Europe/Bratislava'], $unit->options);
        self::assertSame([PHP_BINARY, '/app/spark', 'runwisp:run', 'reports:send', '--no-header'], $unit->argv);
        self::assertSame('skip', $units['cleanup'][0]->options['on_overlap']);
        self::assertSame(2, $units['prune'][0]->options['retry_attempts']);
        self::assertSame('1h', $units['digest'][0]->options['timeout']);
        self::assertCount(2, $warnings);
        self::assertStringContainsString('->named()', $warnings[0]);
        self::assertStringContainsString('"reports:send-2"', $warnings[1]);
    }
}

#[Task('digest', timeout: '1h')]
final class SendDigest
{
}
