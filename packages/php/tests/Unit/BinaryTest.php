<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\Tests\Unit;

use PHPUnit\Framework\TestCase;
use RunWisp\Binary;

final class BinaryTest extends TestCase
{
    public function testMismatchWarnsOnlyForTwoKnownReleases(): void
    {
        self::assertNull(Binary::mismatch('runwisp', '1.8.0', '1.8.0'));
        self::assertNull(Binary::mismatch('runwisp', '1.8.0-dev', '1.7.0'));
        self::assertNull(Binary::mismatch('runwisp', '1.8.0', 'dev-main'));
        self::assertNull(Binary::mismatch('runwisp', null, '1.8.0'));
        self::assertSame(
            'runwisp: runwisp is runwisp 1.7.0 and runwisp/runwisp is 1.8.0: install the same version of both',
            Binary::mismatch('runwisp', '1.7.0', '1.8.0'),
        );
    }
}
