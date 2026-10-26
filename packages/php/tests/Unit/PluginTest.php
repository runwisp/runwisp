<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\Tests\Unit;

use PHPUnit\Framework\TestCase;
use RunWisp\Installer\Plugin;

final class PluginTest extends TestCase
{
    private string $dir;

    protected function setUp(): void
    {
        $this->dir = sys_get_temp_dir() . '/rw-plugin-' . bin2hex(random_bytes(4));
        mkdir($this->dir);
    }

    protected function tearDown(): void
    {
        exec('rm -rf ' . escapeshellarg($this->dir));
    }

    public function testPlatformMatchesTheReleaseNames(): void
    {
        self::assertSame('linux-x64', Plugin::platform('Linux', 'x86_64'));
        self::assertSame('darwin-arm64', Plugin::platform('Darwin', 'arm64'));
        self::assertSame('linux-arm64', Plugin::platform('Linux', 'aarch64'));
        self::assertNull(Plugin::platform('Windows', 'AMD64'));
        self::assertNull(Plugin::platform('Linux', 'i686'));
    }

    public function testChecksumFindsTheArchivesLine(): void
    {
        $sums = str_repeat('a', 64) . "  runwisp-linux-x64.tar.gz\n" . str_repeat('b', 64) . "  runwisp-darwin-arm64.tar.gz\n";
        self::assertSame(str_repeat('b', 64), Plugin::checksum($sums, 'runwisp-darwin-arm64.tar.gz'));
        $this->expectExceptionMessage('no checksum for runwisp-linux-arm64.tar.gz');
        Plugin::checksum($sums, 'runwisp-linux-arm64.tar.gz');
    }

    public function testPlaceVerifiesAndExtractsTheBinary(): void
    {
        $tarball = $this->tarball("#!/bin/sh\necho runwisp\n");
        $target = $this->dir . '/bin/runwisp-bin';

        Plugin::place($tarball, hash('sha256', $tarball), $target);

        self::assertSame("#!/bin/sh\necho runwisp\n", file_get_contents($target));
        self::assertTrue(is_executable($target));
        self::assertSame(['runwisp-bin'], array_values(array_diff((array) scandir($this->dir . '/bin'), ['.', '..'])));
    }

    public function testPlaceRefusesATamperedDownload(): void
    {
        $tarball = $this->tarball('x');
        $this->expectExceptionMessage('does not match');
        Plugin::place($tarball, hash('sha256', 'other'), $this->dir . '/bin/runwisp-bin');
    }

    private function tarball(string $binary): string
    {
        $tar = new \PharData($this->dir . '/build.tar');
        $tar->addFromString('runwisp', $binary);
        $tar->compress(\Phar::GZ);

        return (string) file_get_contents($this->dir . '/build.tar.gz');
    }
}
