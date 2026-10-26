<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\CodeIgniter;

use CodeIgniter\Tasks\Scheduler;
use CodeIgniter\Tasks\Task;
use RunWisp\Names;
use RunWisp\Task as RunWispTask;
use RunWisp\Unit;

/** The tasks of codeigniter4/tasks' Scheduler, as RunWisp tasks. */
final class Tasks
{
    /**
     * Tasks limited by environments() to other environments are left out.
     *
     * @param string                        $environment the app's
     * @param string                        $timezone    the app's, which the scheduler fires in
     * @param string                        $spark       the spark script that runs a task
     * @param array<string, string>         $commands    spark's command classes by name, for their #[Task]
     * @param (\Closure(string): void)|null $warn        told about tasks whose name had to be made up
     *
     * @return array<string, array{Unit, Task}>
     */
    public static function units(Scheduler $scheduler, string $environment, string $timezone, string $spark, array $commands = [], ?\Closure $warn = null): array
    {
        $names = new Names($warn);
        $units = [];
        foreach ($scheduler->getTasks() as $task) {
            if ($task->environments !== [] && !in_array($environment, $task->environments, true)) {
                continue;
            }
            $action = $task->getAction();
            $target = match ($task->getType()) {
                'closure' => $action instanceof \Closure ? $action : null,
                'command' => is_string($action) ? $commands[explode(' ', $action, 2)[0]] ?? null : null,
                default => null,
            };
            $attribute = RunWispTask::of($target);
            $named = (new \ReflectionProperty(Task::class, 'name'))->isInitialized($task) && $task->name !== '' ? $task->name : null;
            $name = $names->claim($named ?? $attribute->name ?? self::rawName($task, dirname($spark), $warn));
            $options = ['cron' => $task->getExpression(), 'timezone' => $timezone];
            if ((new \ReflectionProperty(Task::class, 'singleInstance'))->getValue($task) === true) {
                $options['on_overlap'] = 'skip';
            }
            $options = [...$options, ...$attribute->options ?? []];
            $units[$name] = [new Unit($name, $options, [PHP_BINARY, $spark, 'runwisp:run', $name, '--no-header']), $task];
        }

        return $units;
    }

    /** @param (\Closure(string): void)|null $warn */
    private static function rawName(Task $task, string $root, ?\Closure $warn): string
    {
        $action = $task->getAction();
        return match ($task->getType()) {
            'closure' => self::closureName($action, $root, $warn),
            'queue' => 'queue:' . (is_array($action) ? implode(':', array_filter(array_slice($action, 0, 2), 'is_string')) : ''),
            default => is_string($action) ? $action : $task->getType(),
        };
    }

    /**
     * Where the closure is defined, relative to the app root: stable while
     * that file doesn't move it.
     *
     * @param (\Closure(string): void)|null $warn
     */
    private static function closureName(mixed $action, string $root, ?\Closure $warn): string
    {
        if (!$action instanceof \Closure) {
            return 'closure';
        }
        $ref = new \ReflectionFunction($action);
        $file = (string) $ref->getFileName();
        $where = (str_starts_with($file, "$root/") ? substr($file, strlen($root) + 1) : basename($file)) . ':' . $ref->getStartLine();
        if ($warn !== null) {
            $warn("runwisp: the closure task at $where has no name; name it with ->named() or #[RunWisp\\Task('name')] so its run history survives edits to that file");
        }

        return "closure:$where";
    }
}
