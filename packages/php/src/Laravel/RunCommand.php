<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\Laravel;

use Illuminate\Console\Command;
use Illuminate\Console\Scheduling\CallbackEvent;
use Illuminate\Console\Scheduling\Event;
use Illuminate\Console\Scheduling\Schedule;
use Illuminate\Contracts\Foundation\Application;
use RunWisp\Run;

/** One run of a scheduled task: what `runwisp:work` starts for each run. */
final class RunCommand extends Command
{
    /** @var string */
    protected $signature = 'runwisp:run {name : The task name in RunWisp}';

    /** @var string */
    protected $description = 'Run one scheduled task now, as RunWisp does';

    public function handle(Schedule $schedule): int
    {
        $name = $this->argument('name');
        $name = is_string($name) ? $name : '';
        $event = (ScheduleUnits::map($schedule, $this->laravel)[$name] ?? [])[1] ?? null;
        if ($event === null) {
            $this->error("runwisp: no scheduled task is named \"$name\"");

            return 1;
        }
        if (!$event->runsInMaintenanceMode() && $this->laravel->isDownForMaintenance()) {
            $this->line('runwisp: skipped: the app is in maintenance mode');

            return 0;
        }
        if (!$event->filtersPass($this->laravel)) {
            $this->line('runwisp: skipped: a when() or skip() filter said no');

            return 0;
        }
        $run = Run::current() ?? Run::manual($name);

        return $run->execute(fn (): int => self::runEvent($event, $this->laravel));
    }

    /** Laravel's Event::run(), but a command's output goes to the run's log, not /dev/null. */
    private static function runEvent(Event $event, Application $app): int
    {
        if ($event instanceof CallbackEvent) {
            $event->run($app);
        } elseif (!$event->shouldSkipDueToOverlapping()) {
            $event->callBeforeCallbacks($app);
            $cwd = getcwd();
            chdir($app->basePath());
            passthru((string) $event->command, $code);
            if ($cwd !== false) {
                chdir($cwd);
            }
            $event->finish($app, $code);
        } else {
            $event->skippedBecauseOverlapping = true;
        }
        if ($event->skippedBecauseOverlapping) {
            echo "runwisp: skipped: withoutOverlapping() found it still running\n";

            return 0;
        }

        return (int) $event->exitCode;
    }
}
