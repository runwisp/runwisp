<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\Tests\Unit;

use PHPUnit\Framework\TestCase;
use RunWisp\Check;

final class CheckTest extends TestCase
{
    public function testWhenShowsTheNextRunInTheSchedulesZone(): void
    {
        $schedule = ['description' => 'At 03:00', 'timezone' => 'Europe/Bratislava', 'next' => [['at' => '2026-10-23T01:00:00Z']]];

        self::assertSame(
            '0 3 * * *: At 03:00 (Europe/Bratislava), next Fri 2026-10-23 03:00 CEST',
            Check::when(['cron' => '0 3 * * *'], $schedule),
        );
    }

    public function testWhenShowsSecondsForASixFieldCronHoweverItIsSpaced(): void
    {
        $schedule = ['description' => 'Every 10 seconds', 'timezone' => 'UTC', 'next' => [['at' => '2026-10-23T01:00:10Z']]];

        self::assertStringEndsWith('next Fri 2026-10-23 01:00:10 UTC', Check::when(['cron' => '*/10  * * * * *'], $schedule));
    }

    public function testWhenKeepsTheTimeOfAZonePhpDoesntKnow(): void
    {
        $schedule = ['description' => 'At 03:00', 'timezone' => 'Local', 'next' => [['at' => '2026-10-23T03:00:00+02:00']]];

        self::assertStringEndsWith('(Local), next Fri 2026-10-23 03:00 GMT+0200', Check::when(['cron' => '0 3 * * *'], $schedule));
    }

    public function testWhenWithoutCron(): void
    {
        self::assertSame('Runs only when started from the web UI, the TUI or the CLI', Check::when([], null));
    }
}
