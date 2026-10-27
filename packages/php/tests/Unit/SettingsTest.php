<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\Tests\Unit;

use PHPUnit\Framework\TestCase;
use RunWisp\Settings;

final class SettingsTest extends TestCase
{
    public function testSocketMovesOutOfADataDirTooLongForIt(): void
    {
        self::assertSame('/srv/app/.runwisp/runwisp.sock', self::at('/srv/app/.runwisp')->socket);
        $long = '/srv/' . str_repeat('d', 120);
        $socket = self::at($long)->socket;
        self::assertLessThanOrEqual(100 + strlen(sys_get_temp_dir()), strlen($socket));
        self::assertMatchesRegularExpression('#/runwisp-\d+/daemon-[0-9a-f]{12}\.sock$#', $socket);
    }

    public function testUrlShowsALoopbackForAWildcardHost(): void
    {
        self::assertSame('http://127.0.0.1:9000', self::at('/d', '0.0.0.0')->url(9000));
        self::assertSame('http://[::1]:9477', self::at('/d', '::1')->url());
    }

    public function testResolveRefusesAPasswordWithAuthOff(): void
    {
        $this->expectExceptionMessage('password and auth: false');
        Settings::resolve(['auth' => false, 'password' => 'x', 'binary' => 'runwisp']);
    }

    public function testResolveMakesTheDataDirAbsolute(): void
    {
        $settings = Settings::resolve(['data' => '../var/./rw', 'binary' => 'runwisp', 'app' => 'a'], null, '/srv/app/public');
        self::assertSame('/srv/app/var/rw', $settings->data);
        self::assertSame('/srv/app/public/runwisp.toml', $settings->config);
    }

    public function testAppNameIsTheNearestComposerName(): void
    {
        self::assertSame('runwisp/runwisp', Settings::defaultAppName(__DIR__));
    }

    private static function at(string $data, string $host = '127.0.0.1'): Settings
    {
        return new Settings('app', $data, null, $host, null, true, 'runwisp', '/tmp');
    }
}
