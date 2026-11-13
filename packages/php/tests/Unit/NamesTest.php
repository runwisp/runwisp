<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\Tests\Unit;

use PHPUnit\Framework\TestCase;
use RunWisp\Names;

final class NamesTest extends TestCase
{
    public function testSlugKeepsWhatRunWispAccepts(): void
    {
        self::assertSame('emails:send', Names::slug('emails:send'));
        self::assertSame('Closure-at:-routes-console.php:12', Names::slug('Closure at: routes/console.php:12'));
        self::assertSame('task', Names::slug('///'));
        self::assertSame(100, strlen(Names::slug(str_repeat('x', 150))));
    }

    public function testValidIsWhatNeedsNoSlug(): void
    {
        self::assertTrue(Names::valid('nightly-reports'));
        self::assertTrue(Names::valid('emails:send.v2_x'));
        self::assertTrue(Names::valid(str_repeat('x', 100)));
        self::assertFalse(Names::valid(''));
        self::assertFalse(Names::valid(str_repeat('x', 101)));
        self::assertFalse(Names::valid('Email the daily sales digest (production only)'));
        self::assertFalse(Names::valid("backup\n"));
    }

    public function testClaimRenamesACollisionAndWarns(): void
    {
        $warnings = [];
        $names = new Names(function (string $w) use (&$warnings): void {
            $warnings[] = $w;
        });
        self::assertSame('backup', $names->claim('backup'));
        self::assertSame('backup-2', $names->claim('backup'));
        self::assertSame('backup-3', $names->claim('backup'));
        self::assertCount(2, $warnings);
        self::assertStringContainsString('"backup-2"', $warnings[0]);
    }
}
