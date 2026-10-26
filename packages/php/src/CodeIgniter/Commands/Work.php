<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\CodeIgniter\Commands;

use CodeIgniter\CLI\BaseCommand;
use CodeIgniter\CLI\CLI;
use CodeIgniter\Config\Services;
use CodeIgniter\Tasks\Scheduler;
use CodeIgniter\Tasks\Task;
use RunWisp\CodeIgniter\Config\RunWisp;
use RunWisp\CodeIgniter\Tasks;
use RunWisp\Unit;
use RunWisp\Worker;

/** Runs the app's scheduled tasks under RunWisp, in place of a crontab entry for tasks:run. */
class Work extends BaseCommand
{
    protected $group = 'RunWisp';
    protected $name = 'runwisp:work';
    protected $description = 'Runs the scheduled tasks under RunWisp until stopped.';
    protected $usage = 'runwisp:work';

    /** @param array<int|string, string|null> $params */
    public function run(array $params): int
    {
        return self::worker()->run();
    }

    /** The worker for app/Config/Tasks.php and Config\RunWisp. */
    public static function worker(): Worker
    {
        // By short name, so the app's Config\RunWisp wins over this package's.
        $config = config('RunWisp');
        if (!$config instanceof RunWisp) {
            $config = new RunWisp();
        }
        $log = static function (string $level, string $message): void {
            if ($level === 'error') {
                CLI::error($message);
            } else {
                CLI::write($message, $level === 'warn' ? 'yellow' : null);
            }
        };
        $units = self::units(static fn (string $m) => $log('warn', $m));

        return new Worker($config->settings(), $config->config, array_column($units, 0), $log);
    }

    /**
     * The tasks app/Config/Tasks.php defines, as tasks:run reads them.
     *
     * @param (\Closure(string): void)|null $warn
     *
     * @return array<string, array{Unit, Task}>
     */
    public static function units(?\Closure $warn = null): array
    {
        $scheduler = new Scheduler();
        config('Tasks')->init($scheduler);
        $commands = array_map(static fn (array $command): string => $command['class'], Services::commands()->getCommands());

        return Tasks::units($scheduler, ENVIRONMENT, config('App')->appTimezone, ROOTPATH . 'spark', $commands, $warn);
    }
}
