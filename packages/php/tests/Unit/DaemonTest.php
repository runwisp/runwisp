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

    public function testCallFailsAsAHangUpWhenTheDaemonDiesMidAnswer(): void
    {
        $data = sys_get_temp_dir() . '/rw-unit-' . bin2hex(random_bytes(4));
        mkdir($data);
        $settings = new Settings('app', $data, null, '127.0.0.1', null, true, 'runwisp', '/tmp');
        // A stand-in daemon that announces 100 bytes, sends 7 and hangs up.
        $server = '$s = stream_socket_server("unix://" . $argv[1]); $c = stream_socket_accept($s, 5);'
            . ' fread($c, 8192); fwrite($c, "HTTP/1.0 200 OK\r\nContent-Length: 100\r\n\r\n{\"pid\":"); fclose($c);';
        $proc = proc_open([PHP_BINARY, '-r', $server, $settings->socket], [], $pipes);
        self::assertIsResource($proc);
        try {
            for ($i = 0; $i < 500 && !file_exists($settings->socket); $i++) {
                usleep(10_000);
            }
            $this->expectExceptionMessage('RunWisp hung up during GET /api/daemon/identity');
            (new Daemon($settings))->call('GET', '/api/daemon/identity', 5.0);
        } finally {
            proc_close($proc);
            @unlink($settings->socket);
            @rmdir($data);
        }
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
