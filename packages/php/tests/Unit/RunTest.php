<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\Tests\Unit;

use PHPUnit\Framework\TestCase;
use RunWisp\Run;

final class RunTest extends TestCase
{
    protected function tearDown(): void
    {
        putenv(Run::ENV);
        putenv('RW_TEST_TOKEN');
    }

    public function testCurrentReadsBackWhatTheWorkerPassed(): void
    {
        self::assertNull(Run::current());
        putenv('RW_TEST_TOKEN=t0ken');
        putenv(Run::ENV . '=' . Run::payload('01RUN', 'backup', ['RW_TEST_TOKEN' => 't0ken'], ['DRY_RUN' => '1'], '2026-10-21T03:00:00Z'));

        $run = Run::current();
        self::assertInstanceOf(Run::class, $run);
        self::assertSame('01RUN', $run->id);
        self::assertSame('backup', $run->task);
        self::assertSame(['DRY_RUN' => '1'], $run->params);
        self::assertSame(['RW_TEST_TOKEN' => 't0ken'], $run->env);
        self::assertSame('2026-10-21T03:00:00+00:00', $run->scheduledAt?->format(DATE_ATOM));
    }

    public function testSkipLeavesTheFileTheWorkerLooksFor(): void
    {
        $file = sys_get_temp_dir() . '/rwp-run-' . bin2hex(random_bytes(4)) . '.skip';
        putenv(Run::ENV . '=' . Run::payload('01RUN', 'backup', [], [], null, $file));
        $run = Run::current();
        self::assertSame($file, $run?->skipFile);
        ob_start();
        try {
            self::assertSame(0, $run->skip('nothing to do'));
            self::assertFileExists($file);
        } finally {
            $output = ob_get_clean();
            @unlink($file);
        }
        self::assertSame("runwisp: skipped: nothing to do\n", $output);
    }

    public function testSkipWritesTheFileOnlyForARunRunWispStarted(): void
    {
        $file = sys_get_temp_dir() . '/rwp-run-' . bin2hex(random_bytes(4)) . '.skip';
        ob_start();
        try {
            self::assertSame(0, (new Run('01RUN', 'backup', skipFile: $file))->skip('why'));
            self::assertFileExists($file);
            self::assertSame(0, Run::manual('backup')->skip('why'));
        } finally {
            ob_end_clean();
            @unlink($file);
        }
        self::assertNull(Run::manual('backup')->skipFile);
    }

    public function testAManualRunHasNoId(): void
    {
        self::assertSame(['', 'backup'], [Run::manual('backup')->id, Run::manual('backup')->task]);
    }

    public function testExecuteTurnsTheOutcomeIntoAnExitCode(): void
    {
        $run = new Run('01RUN', 'backup');
        self::assertSame(0, $run->execute(fn () => 'ignored'));
        self::assertSame(3, $run->execute(fn () => 3));
        self::assertSame(1, $run->execute(function (): never {
            throw new \RuntimeException('boom');
        }));
    }
}
