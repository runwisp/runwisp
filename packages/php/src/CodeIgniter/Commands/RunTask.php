<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\CodeIgniter\Commands;

use CodeIgniter\CLI\BaseCommand;
use CodeIgniter\CLI\CLI;
use RunWisp\Run;

/** Runs one scheduled task now: runwisp:work starts it for each run. */
class RunTask extends BaseCommand
{
    protected $group = 'RunWisp';
    protected $name = 'runwisp:run';
    protected $description = 'Runs one scheduled task now, as RunWisp does.';
    protected $usage = 'runwisp:run <name>';
    protected $arguments = ['name' => 'The task name, as RunWisp shows it'];

    /** @param array<int|string, string|null> $params */
    public function run(array $params): int
    {
        $name = (string) ($params[0] ?? '');
        $task = Work::units()[$name][1] ?? null;
        if ($task === null) {
            CLI::error("runwisp: no scheduled task is named \"$name\"");

            return 1;
        }
        // Run by hand, without RunWisp, it still runs.
        return (Run::current() ?? Run::manual($name))->execute(static function () use ($task): void {
            self::print($task->run());
        });
    }

    /** A task's result is its output: a command's buffered text, a shell's lines, a URL's body. */
    private static function print(mixed $result): void
    {
        // ponytail: command output arrives when the command ends; run it through service('commands') to stream it.
        if (is_array($result)) {
            $result = implode("\n", array_filter($result, 'is_scalar'));
        }
        if ((is_string($result) || $result instanceof \Stringable) && (string) $result !== '') {
            $text = (string) $result;
            echo str_ends_with($text, "\n") ? $text : "$text\n";
        }
    }
}
