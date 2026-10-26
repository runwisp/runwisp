<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\Tests\E2E;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;
use Symfony\Component\Process\Process;

/** `php script.php check` with the real runwisp binary, and `php script.php run`. */
final class CheckTest extends TestCase
{
    public function testListsEachTaskWithItsScheduleAndOptions(): void
    {
        $check = self::check([]);

        self::assertSame(0, $check->getExitCode(), $check->getErrorOutput());
        self::assertMatchesRegularExpression(
            '/^nightly-backup\n  0 3 \* \* \*: At 03:00 \(UTC\), next \w{3} \d{4}-\d\d-\d\d 03:00 UTC\n  retry_attempts = 3\n'
            . 'by-hand\n  Runs only when started from the web UI, the TUI or the CLI\n'
            . 'queue \(service\)\n  restart_delay = "5s"\n'
            . 'runwisp: RunWisp accepts the config: 2 tasks, 1 service\n$/',
            $check->getOutput(),
        );
        self::assertSame('', $check->getErrorOutput());
    }

    /** @param list<string> $args */
    #[DataProvider('checkAndWorker')]
    public function testFailsWithRunWispsErrorForAMisspelledOption(array $args): void
    {
        $check = self::check(['RUNWISP_TEST_TYPO' => '1'], $args);

        self::assertSame(1, $check->getExitCode());
        self::assertSame('', $check->getOutput());
        // RunWisp's error spans lines: each stays on its own, indented under the first.
        self::assertMatchesRegularExpression(
            '/^runwisp: RunWisp rejects the config:\n  failed to parse config file:\n    unknown key "retry_attemps" at tasks\.nightly-backup\.retry_attemps \(did you mean "retry_attempts"\?\)\n/',
            $check->getErrorOutput(),
        );
    }

    /** @return array<string, array{list<string>}> */
    public static function checkAndWorker(): array
    {
        // The worker checks first, so with a bad config it exits before starting RunWisp.
        return ['check' => [['check']], 'worker' => [[]]];
    }

    public function testRunsOneTaskByHand(): void
    {
        $run = self::check([], ['run', 'by-hand']);

        self::assertSame(0, $run->getExitCode(), $run->getErrorOutput());
        self::assertSame("ran by hand\n", $run->getOutput());
    }

    /**
     * @param array<string, string> $env
     * @param list<string>          $args
     */
    private static function check(array $env, array $args = ['check']): Process
    {
        $process = new Process([PHP_BINARY, __DIR__ . '/fixtures/check.php', ...$args], null, [
            'RUNWISP_PHP_TEST_BINARY' => getenv('RUNWISP_PHP_TEST_BINARY') ?: 'runwisp',
            ...$env,
        ], 30);
        $process->run();

        return $process;
    }
}
