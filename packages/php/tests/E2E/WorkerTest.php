<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\Tests\E2E;

use PHPUnit\Framework\Attributes\Depends;
use PHPUnit\Framework\TestCase;
use RunWisp\Daemon;
use RunWisp\Settings;
use Symfony\Component\Process\Process;

/** A worker script with the real runwisp binary. */
final class WorkerTest extends TestCase
{
    private static string $data;
    private static Process $worker;
    private static Daemon $daemon;

    public static function setUpBeforeClass(): void
    {
        self::$data = sys_get_temp_dir() . '/rwp-e2e-' . bin2hex(random_bytes(4));
        $server = stream_socket_server('tcp://127.0.0.1:0');
        self::assertNotFalse($server);
        $port = (int) substr((string) strrchr((string) stream_socket_get_name($server, false), ':'), 1);
        fclose($server);
        self::$worker = new Process([PHP_BINARY, __DIR__ . '/fixtures/worker.php'], null, [
            'RUNWISP_DATA' => self::$data,
            'RUNWISP_PORT' => (string) $port,
            'RUNWISP_PHP_TEST_BINARY' => getenv('RUNWISP_PHP_TEST_BINARY') ?: 'runwisp',
        ]);
        self::$worker->start();
        self::$daemon = new Daemon(Settings::resolve(['data' => self::$data, 'binary' => 'runwisp', 'app' => 'e2e']));
        self::waitFor(fn (): bool => self::$daemon->healthy() && self::serviceRun() !== null, 'the daemon and the service to start');
    }

    public static function tearDownAfterClass(): void
    {
        if (self::$worker->isRunning()) {
            self::$worker->stop(0);
        }
        self::$daemon->stop();
        exec('rm -rf ' . escapeshellarg(self::$data));
    }

    public function testTaskOutputIsTheRunsLog(): void
    {
        $run = self::trigger('hello');
        self::assertSame(0, $run['exitCode']);
        self::assertSame("hello from the app\n[ERR] and a warning\nno newline\n", $run['log']);
    }

    public function testRunGetsItsEnvAndParams(): void
    {
        self::assertSame("region=eu token=t0ken dry=1\n", self::trigger('context')['log']);
    }

    public function testThrowingFailsTheRunWithTheTrace(): void
    {
        $run = self::trigger('broken');
        self::assertSame(1, $run['exitCode']);
        self::assertStringContainsString('[ERR] RuntimeException: boom', $run['log']);
    }

    public function testReturnedIntIsTheExitCode(): void
    {
        self::assertSame(3, self::trigger('exits')['exitCode']);
    }

    public function testShellTasksFromTheConfigRunInTheDaemon(): void
    {
        self::assertSame("from a shell task\n", self::trigger('shell')['log']);
    }

    public function testScheduledTaskRuns(): void
    {
        self::waitFor(function (): bool {
            foreach (self::runs() as $run) {
                if ($run['taskName'] === 'tick' && ($run['exitCode'] ?? null) === 0) {
                    return true;
                }
            }

            return false;
        }, 'a scheduled tick');
        $this->addToAssertionCount(1);
    }

    public function testStoppingAServiceReachesItsCallback(): void
    {
        $id = self::serviceRun();
        self::assertNotNull($id);
        self::assertSame(204, self::$daemon->call('POST', "/api/runs/$id/stop")['status']);
        self::waitFor(fn (): bool => (self::$daemon->call('GET', "/api/runs/$id")['body']['status'] ?? '') === 'ended', 'the service run to end');
        self::assertSame("worker up\nworker stopping\n", self::$daemon->call('GET', "/api/runs/$id/log/raw")['body']);
        self::assertSame('stopped', self::$daemon->call('GET', "/api/runs/$id")['body']['endReason']);
    }

    #[Depends('testStoppingAServiceReachesItsCallback')]
    public function testSigtermLeavesAndTheDaemonStops(): void
    {
        $pid = self::$daemon->identity()['pid'];
        self::$worker->signal(15);
        self::waitFor(fn (): bool => !self::$worker->isRunning(), 'the worker to exit');
        self::assertSame(143, self::$worker->getExitCode(), self::$worker->getErrorOutput());
        self::waitFor(fn (): bool => !Daemon::alive($pid), 'the daemon to exit');
        self::assertStringContainsString('RunWisp dashboard: http://127.0.0.1:', self::$worker->getOutput());
    }

    /** @return array{exitCode: mixed, log: mixed} */
    private static function trigger(string $task): array
    {
        $run = self::$daemon->call('POST', "/api/tasks/$task/run?wait=true", 30.0)['body'];
        self::assertIsArray($run, "triggering $task");

        return ['exitCode' => $run['exitCode'] ?? null, 'log' => self::$daemon->call('GET', "/api/runs/{$run['id']}/log/raw")['body']];
    }

    /** @return list<array<string, mixed>> */
    private static function runs(): array
    {
        $body = self::$daemon->call('GET', '/api/runs?limit=200')['body'];

        return is_array($body) ? $body['items'] : [];
    }

    private static function serviceRun(): ?string
    {
        foreach (self::runs() as $run) {
            if ($run['taskName'] === 'worker' && $run['status'] === 'running') {
                return $run['id'];
            }
        }

        return null;
    }

    private static function waitFor(callable $done, string $what): void
    {
        for ($deadline = microtime(true) + 20; microtime(true) < $deadline; usleep(100_000)) {
            if ($done()) {
                return;
            }
        }
        self::fail("timed out waiting for $what\nworker stderr:\n" . self::$worker->getErrorOutput());
    }
}
