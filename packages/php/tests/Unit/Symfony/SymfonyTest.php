<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\Tests\Unit\Symfony;

use PHPUnit\Framework\TestCase;
use RunWisp\Symfony\Schedules;
use RunWisp\Unit;
use Symfony\Bundle\FrameworkBundle\Console\Application;
use Symfony\Component\Console\Tester\CommandTester;
use Symfony\Component\DependencyInjection\ContainerInterface;

require_once __DIR__ . '/Fixtures.php';

final class SymfonyTest extends TestCase
{
    private static TestKernel $kernel;

    public static function setUpBeforeClass(): void
    {
        (new \Symfony\Component\Filesystem\Filesystem())->remove(sys_get_temp_dir() . '/runwisp-symfony-test');
        self::$kernel = new TestKernel('test', false);
        self::$kernel->boot();
    }

    public function testMapsTheSchedulesToTasks(): void
    {
        $warnings = [];
        $container = self::$kernel->getContainer()->get('test.service_container');
        self::assertInstanceOf(ContainerInterface::class, $container);
        $schedules = $container->get('runwisp.schedules');
        self::assertInstanceOf(Schedules::class, $schedules);
        $units = $schedules->units(['php', 'bin/console'], new Application(self::$kernel), static function (string $w) use (&$warnings): void {
            $warnings[] = $w;
        });
        $byName = array_combine(array_map(static fn (Unit $u) => $u->name, $units), $units);

        self::assertSame(['cron' => '0 3 * * *', 'timezone' => 'Europe/Bratislava', 'jitter' => '30s', 'retry_attempts' => 2], $byName['nightly']->options);
        self::assertSame(['php', 'bin/console', 'runwisp:run', 'nightly'], $byName['nightly']->argv);
        self::assertSame(['cron' => '@every 90s', 'timeout' => '5m'], $byName['clean']->options);
        self::assertSame(['cron' => '*/5 * * * *'], $byName['reports.Ping']->options);
        self::assertCount(3, $units);
        // "1 month" has no fixed length, and a callback trigger no schedule RunWisp can run.
        self::assertCount(2, $warnings);
        self::assertStringContainsString('schedule "reports"', $warnings[0]);
    }

    public function testRunsACommandWithItsOutput(): void
    {
        $tester = $this->runCommand('nightly');

        self::assertSame(0, $tester->getStatusCode());
        self::assertStringContainsString('nightly ran', $tester->getDisplay());
    }

    public function testHandlesOtherMessagesHere(): void
    {
        $this->runCommand('clean');
        $this->runCommand('reports.Ping');

        self::assertSame(1, Cleaner::$calls);
        self::assertSame(1, PingHandler::$calls);
    }

    public function testFailsForAnUnknownName(): void
    {
        self::assertSame(1, $this->runCommand('nope')->getStatusCode());
    }

    private function runCommand(string $name): CommandTester
    {
        $application = new Application(self::$kernel);
        $tester = new CommandTester($application->find('runwisp:run'));
        $tester->execute(['name' => $name]);

        return $tester;
    }
}
