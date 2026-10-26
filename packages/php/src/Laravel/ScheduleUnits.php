<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\Laravel;

use Illuminate\Console\Scheduling\CallbackEvent;
use Illuminate\Console\Scheduling\Event;
use Illuminate\Console\Scheduling\Schedule;
use Illuminate\Contracts\Console\Kernel;
use Illuminate\Contracts\Foundation\Application;
use Illuminate\Foundation\Console\ClosureCommand;
use RunWisp\Names;
use RunWisp\Task;
use RunWisp\Unit;

/** Laravel's schedule as RunWisp tasks. */
final class ScheduleUnits
{
    /**
     * Every event that runs in this environment, by its task name. Both the
     * worker and the run it starts map the same schedule, so the names agree.
     *
     * @param (\Closure(string): void)|null $warn
     *
     * @return array<string, array{Unit, Event}>
     */
    public static function map(Schedule $schedule, Application $app, ?\Closure $warn = null): array
    {
        $names = new Names($warn);
        $units = [];
        $commands = null;
        foreach ($schedule->events() as $event) {
            // ->environments(): the other environments' events aren't this app's tasks.
            if ($event->environments !== [] && !$app->environment($event->environments)) {
                continue;
            }
            [$target, $method, $default, $own] = self::subject($event, $app->basePath(), $commands ??= self::commands($app));
            $task = Task::of($target, $method);
            // name() sets the description, which Laravel also fills itself: that is no name.
            $named = is_string($event->description) && $event->description !== '' && $event->description !== $own ? $event->description : $task?->name;
            if ($named === null && $target instanceof \Closure && $event instanceof CallbackEvent && $warn !== null) {
                $warn("runwisp: a scheduled closure at $default has no name, so its run history is lost when that line moves; name it with ->name() or #[RunWisp\\Task('name')]");
            }
            $name = $names->claim($named ?? $default);
            $options = [...self::options($event), ...$task->options ?? []];
            $units[$name] = [new Unit($name, $options, [PHP_BINARY, $app->basePath('artisan'), 'runwisp:run', $name]), $event];
        }

        return $units;
    }

    /** @return array<string, string> */
    public static function options(Event $event): array
    {
        $options = ['cron' => $event->expression];
        if ($event->repeatSeconds !== null) {
            // everyTenSeconds() and the like: a leading seconds field.
            $options['cron'] = "*/{$event->repeatSeconds} {$event->expression}";
        }
        if ($event->timezone !== null) {
            $options['timezone'] = $event->timezone instanceof \DateTimeZone ? $event->timezone->getName() : (string) $event->timezone;
        }
        if ($event->withoutOverlapping) {
            $options['on_overlap'] = 'skip';
        }

        return $options;
    }

    /**
     * What the event runs, where a #[Task] on it would be, the task name
     * made from it (the artisan command, the job or callable's class, the
     * shell command, else where the closure is), and the description Laravel
     * gives the event itself: a command's description, or a job's class.
     *
     * @param array<string, \Symfony\Component\Console\Command\Command> $commands artisan's, by name
     *
     * @return array{\Closure|string|null, ?string, string, ?string}
     */
    private static function subject(Event $event, string $base, array $commands): array
    {
        if (is_string($event->command) && $event->command !== '') {
            // '/usr/bin/php' 'artisan' inspire --quiet → inspire
            if (preg_match("/\\bartisan'?\\s+'?([^\\s']+)/", $event->command, $m) !== 1) {
                return [null, null, $event->command, null];
            }
            $command = $commands[$m[1]] ?? null;
            $target = $command instanceof ClosureCommand ? (new \ReflectionProperty($command, 'callback'))->getValue($command) : $command;

            return [$target instanceof \Closure ? $target : ($target === null ? null : $target::class), null, $m[1], $command?->getDescription()];
        }
        $callback = $event instanceof CallbackEvent ? (new \ReflectionProperty($event, 'callback'))->getValue($event) : null;
        if ($callback instanceof \Closure) {
            $fn = new \ReflectionFunction($callback);
            // Schedule::job() wraps the job in a closure of its own.
            $job = $fn->getClosureScopeClass()?->getName() === Schedule::class ? ($fn->getClosureUsedVariables()['job'] ?? null) : null;
            if (is_object($job) || (is_string($job) && class_exists($job))) {
                $own = is_string($job) ? $job : (method_exists($job, 'displayName') ? $job->displayName() : $job::class);
                $job = is_object($job) ? $job::class : $job;

                return [$job, 'handle', self::basename($job), $own];
            }

            return [$callback, null, str_replace($base . '/', '', (string) $fn->getFileName()) . ':' . $fn->getStartLine(), null];
        }
        if (is_string($callback) && str_contains($callback, '@')) {
            $callback = explode('@', $callback, 2);
        }
        [$class, $method] = match (true) {
            is_array($callback) => [is_object($callback[0]) ? $callback[0]::class : (string) $callback[0], (string) $callback[1]],
            is_object($callback) => [$callback::class, '__invoke'],
            is_string($callback) => [$callback, '__invoke'],
            default => [null, null],
        };
        if ($class === null) {
            return [null, null, $event->getSummaryForDisplay(), null];
        }

        return [$class, $method, self::basename($class) . (in_array($method, ['__invoke', 'handle'], true) ? '' : ".$method"), null];
    }

    /** @return array<string, \Symfony\Component\Console\Command\Command> */
    private static function commands(Application $app): array
    {
        return $app->make(Kernel::class)->all();
    }

    private static function basename(string $class): string
    {
        return substr((string) strrchr('\\' . $class, '\\'), 1);
    }
}
