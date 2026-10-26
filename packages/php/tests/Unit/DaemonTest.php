<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\Tests\Unit;

use PHPUnit\Framework\TestCase;
use RunWisp\Daemon;
use RunWisp\Settings;

final class DaemonTest extends TestCase
{
    protected function tearDown(): void
    {
        putenv('RW_TEST_UNRELATED');
    }

    public function testProcStateReadsPastAParenInTheCommandName(): void
    {
        self::assertSame('Z', Daemon::procState('42 (a) b) Z 1 42'));
        self::assertSame('S', Daemon::procState('42 (php) S 1 42'));
    }

    public function testFingerprintIgnoresUnrelatedEnvUnlessAShellTaskSeesIt(): void
    {
        $daemon = new Daemon(new Settings('app', '/tmp/rw-unit', null, '127.0.0.1', null, true, 'runwisp', '/tmp'));
        $sdkOnly = '{"tasks":{"a":{"sdk":true}}}';
        $shell = '{"tasks":{"a":{"run":"echo"}}}';
        $before = [$daemon->fingerprint($sdkOnly), $daemon->fingerprint($shell)];
        putenv('RW_TEST_UNRELATED=1');
        self::assertSame($before[0], $daemon->fingerprint($sdkOnly));
        self::assertNotSame($before[1], $daemon->fingerprint($shell));
        self::assertNotSame($before[0], $daemon->fingerprint('{"tasks":{"a":{"sdk":true,"env":{"X":"${RW_TEST_UNRELATED}"}}}}'));
    }
}
