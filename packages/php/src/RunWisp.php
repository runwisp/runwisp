<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp;

/**
 * Tasks and services defined in a plain PHP script:
 *
 *     $runwisp = new RunWisp\RunWisp();
 *     $runwisp->task('nightly-backup', ['cron' => '0 3 * * *'], function (Run $run) { ... });
 *     exit($runwisp->main());
 *
 * `php script.php` is the worker. It runs each task in a new `php script.php`
 * process, where main() calls the task's callback instead. `php script.php
 * check` checks the config and lists the tasks without running anything, and
 * `php script.php run <name>` runs one task in the terminal.
 *
 * @phpstan-import-type Config from Schema
 * @phpstan-import-type TaskConfig from Schema
 * @phpstan-import-type ServiceConfig from Schema
 */
final class RunWisp
{
    /** @var array<string, array{Unit, callable(Run): mixed}> */
    private array $units = [];

    /**
     * @param array{app?: string, data?: string, port?: int, host?: string, password?: string, auth?: bool, binary?: string, config?: Config} $options Settings::OPTIONS, and the rest of a runwisp.toml as config
     */
    public function __construct(private readonly array $options = [])
    {
    }

    /**
     * A task: $options are [tasks.*] keys, as in runwisp.toml. Without cron,
     * it runs only when started from the web UI, the TUI or the CLI.
     *
     * @param TaskConfig           $options
     * @param callable(Run): mixed $fn
     */
    public function task(string $name, array $options, callable $fn): self
    {
        $this->units[$name] = [new Unit($name, $options, self::self()), $fn];

        return $this;
    }

    /**
     * A service: a callback that keeps running, restarted by RunWisp when it
     * returns or throws. $options are [services.*] keys.
     *
     * @param ServiceConfig        $options
     * @param callable(Run): mixed $fn
     */
    public function service(string $name, array $options, callable $fn): self
    {
        $this->units[$name] = [new Unit($name, $options, self::self(), true), $fn];

        return $this;
    }

    /**
     * Runs the worker, or in a run's process, that run. With arguments,
     * `check` checks the config and `run <name>` runs one task here, outside
     * RunWisp. Returns the exit code.
     */
    public function main(): int
    {
        $args = $_SERVER['argv'] ?? [];
        $run = Run::current() ?? (($args[1] ?? null) === 'run' && isset($args[2]) ? Run::manual((string) $args[2]) : null);
        if ($run !== null) {
            $unit = $this->units[$run->task] ?? null;
            if ($unit === null) {
                fwrite(STDERR, "runwisp: this script doesn't define \"{$run->task}\"\n");

                return 1;
            }

            return $run->execute($unit[1]);
        }
        $options = $this->options;
        $config = $options['config'] ?? [];
        unset($options['config']);
        $worker = new Worker(Settings::resolve($options), $config, array_column($this->units, 0));

        return ($args[1] ?? null) === 'check' ? $worker->check() : $worker->run();
    }

    /** @return list<string> this script, run again by the same PHP */
    private static function self(): array
    {
        $script = realpath((string) ($_SERVER['SCRIPT_FILENAME'] ?? ''));

        return [PHP_BINARY, $script === false ? (string) ($_SERVER['SCRIPT_FILENAME'] ?? '') : $script];
    }
}
