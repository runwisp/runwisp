<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\Tests\Unit\Laravel;

use Illuminate\Console\Command;
use Illuminate\Console\Scheduling\Schedule;
use Illuminate\Foundation\Application;
use Illuminate\Support\Facades\Artisan;
use Orchestra\Testbench\TestCase;
use RunWisp\Laravel\RunWispServiceProvider;
use RunWisp\Laravel\ScheduleUnits;
use RunWisp\Names;
use RunWisp\Task;

final class LaravelTest extends TestCase
{
    protected function getPackageProviders($app): array
    {
        return [RunWispServiceProvider::class];
    }

    private function laravel(): Application
    {
        self::assertNotNull($this->app);

        return $this->app;
    }

    private function schedule(): Schedule
    {
        return $this->laravel()->make(Schedule::class);
    }

    public function testRunIsListedSinceTheDocsSayToRunItByHand(): void
    {
        self::assertFalse(Artisan::all()['runwisp:run']->isHidden());
    }

    public function testMapsEventsToTasks(): void
    {
        $schedule = $this->schedule();
        $schedule->command('inspire --quiet')->dailyAt('03:00')->timezone('Europe/Bratislava');
        $schedule->call(#[Task(retry_attempts: 2)] static fn () => null)->name('cleanup')->everyTenSeconds()->withoutOverlapping();
        $schedule->exec('echo hi')->hourly();
        $schedule->call(static fn () => null)->hourly()->environments(['production']);
        $warnings = [];

        $units = ScheduleUnits::map($schedule, $this->laravel(), static function (string $m) use (&$warnings): void {
            $warnings[] = $m;
        });

        self::assertSame(['inspire', 'cleanup', 'echo-hi'], array_keys($units));
        self::assertSame(['cron' => '0 3 * * *', 'timezone' => 'Europe/Bratislava'], $units['inspire'][0]->options);
        // Laravel gives every event the app's schedule timezone, so it fires as schedule:run would.
        self::assertSame(['cron' => '*/10 * * * * *', 'timezone' => 'UTC', 'on_overlap' => 'skip', 'retry_attempts' => 2], $units['cleanup'][0]->options);
        self::assertSame([PHP_BINARY, $this->laravel()->basePath('artisan'), 'runwisp:run', 'cleanup'], $units['cleanup'][0]->argv);
        self::assertSame([], $warnings);
    }

    public function testCommandsAndJobsTakeTheirNameAndKeysFromTheirClass(): void
    {
        Artisan::registerCommand(new SendReports());
        $schedule = $this->schedule();
        // By class, Laravel gives the event the command's description: that is no name.
        $schedule->command(SendReports::class)->daily();
        $schedule->command('inspire')->hourly();
        $schedule->job(new PruneUsers())->daily();
        $schedule->call([new Reports(), 'weekly'])->weekly();

        $units = ScheduleUnits::map($schedule, $this->laravel());

        self::assertSame(['reports', 'inspire', 'PruneUsers', 'Reports.weekly'], array_keys($units));
        self::assertSame(['cron' => '0 0 * * *', 'timezone' => 'UTC', 'timeout' => '10m'], $units['reports'][0]->options);
        self::assertSame(['cron' => '0 0 * * *', 'timezone' => 'UTC', 'retry_attempts' => 1], $units['PruneUsers'][0]->options);
    }

    public function testNameNamesTheTask(): void
    {
        Artisan::registerCommand(new SendReports());
        $schedule = $this->schedule();
        $schedule->command(SendReports::class)->name('nightly-reports')->daily();
        $schedule->command('inspire')->name('quote')->hourly();
        $schedule->job(new PruneUsers())->name('prune')->daily();
        $schedule->call(#[Task('ignored')] static fn () => null)->name('Clean up')->hourly();
        $warnings = [];

        $units = ScheduleUnits::map($schedule, $this->laravel(), static function (string $m) use (&$warnings): void {
            $warnings[] = $m;
        });

        self::assertSame(['nightly-reports', 'quote', 'prune', 'Clean-up'], array_keys($units));
        // The class's #[Task] keys still apply under the name() name.
        self::assertSame(['cron' => '0 0 * * *', 'timezone' => 'UTC', 'timeout' => '10m'], $units['nightly-reports'][0]->options);
        self::assertSame([], $warnings);
    }

    public function testUnnamedClosuresAreNamedByTheirLineAndWarn(): void
    {
        $schedule = $this->schedule();
        $line = __LINE__ + 1;
        $schedule->call(static fn () => null)->hourly();
        $schedule->command('inspire')->hourly();
        $schedule->command('inspire')->daily();
        $warnings = [];

        $units = ScheduleUnits::map($schedule, $this->laravel(), static function (string $m) use (&$warnings): void {
            $warnings[] = $m;
        });

        $names = array_keys($units);
        self::assertSame(Names::slug(__FILE__ . ":$line"), $names[0]);
        self::assertSame(['inspire', 'inspire-2'], array_slice($names, 1));
        self::assertCount(2, $warnings);
        self::assertStringContainsString("has no name, so its run history is lost when that line moves; name it with ->name() or #[RunWisp\\Task('name')]", $warnings[0]);
        self::assertStringContainsString('"inspire-2"', $warnings[1]);
    }

    public function testRunsAClosure(): void
    {
        $this->schedule()->call(#[Task('cleanup')] static function (): void {
            echo "cleaned\n";
        });

        self::assertSame(0, $this->runTask('cleanup', $output));
        self::assertStringContainsString('cleaned', $output);
    }

    public function testAThrowingClosureFailsTheRun(): void
    {
        $this->schedule()->call(#[Task('broken')] static function (): void {
            throw new \RuntimeException('boom');
        });

        self::assertSame(1, $this->runTask('broken', $output));
    }

    public function testRunsACommandWithItsOutputAndCallbacks(): void
    {
        $after = null;
        $this->schedule()->exec('echo from-the-shell; exit 3')
            ->after(static function () use (&$after): void {
                $after = 'called';
            });

        self::assertSame(3, $this->runTask('echo-from-the-shell-exit-3', $output));
        self::assertStringContainsString('from-the-shell', $output);
        self::assertSame('called', $after);
    }

    public function testFiltersSkipTheRun(): void
    {
        $this->schedule()->call(#[Task('filtered')] static function (): void {
            echo "ran\n";
        })->when(false);

        self::assertSame(0, $this->runTask('filtered', $output));
        self::assertStringNotContainsString('ran', $output);
    }

    public function testUnknownTaskFails(): void
    {
        self::assertSame(1, $this->runTask('nope', $output));
    }

    /**
     * Runs `runwisp:run name` and returns its exit code; passthru and echo go through PHP's output.
     *
     * @param-out string $output
     */
    private function runTask(string $name, ?string &$output): int
    {
        ob_start();
        try {
            $code = Artisan::call('runwisp:run', ['name' => $name]);
        } finally {
            $output = ob_get_clean() . Artisan::output();
        }

        return $code;
    }
}

#[Task('reports', timeout: '10m')]
final class SendReports extends Command
{
    protected $signature = 'reports:send';
    protected $description = 'Send the daily reports';

    public function handle(): void
    {
    }
}

#[Task(retry_attempts: 1)]
final class PruneUsers
{
    public function handle(): void
    {
    }
}

final class Reports
{
    public function weekly(): void
    {
    }
}
