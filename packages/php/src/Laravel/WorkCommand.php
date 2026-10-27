<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\Laravel;

use Illuminate\Console\Command;
use Illuminate\Console\Scheduling\Schedule;
use RunWisp\Settings;
use RunWisp\Worker;

/** Runs the schedule under RunWisp, in place of `schedule:run` from cron. */
class WorkCommand extends Command
{
    /** @var string */
    protected $signature = 'runwisp:work';

    /** @var string */
    protected $description = 'Run the scheduled tasks under RunWisp, which records every run';

    public function handle(Schedule $schedule): int
    {
        return $this->worker($schedule)->run();
    }

    /** The worker for this app's schedule and config/runwisp.php. */
    protected function worker(Schedule $schedule): Worker
    {
        $log = function (string $level, string $message): void {
            match ($level) {
                'error' => $this->error($message),
                'warn' => $this->warn($message),
                default => $this->line($message),
            };
        };
        $units = ScheduleUnits::map($schedule, $this->laravel, static fn (string $message) => $log('warn', $message));
        $config = (array) $this->laravel->make('config')->get('runwisp', []);
        $options = array_filter([
            'app' => self::string($config['app'] ?? null),
            'data' => self::string($config['data'] ?? null),
            'port' => is_numeric($config['port'] ?? null) ? (int) $config['port'] : null,
            'host' => self::string($config['host'] ?? null),
            'password' => self::string($config['password'] ?? null),
            'auth' => is_bool($config['auth'] ?? null) ? $config['auth'] : null,
            'binary' => self::string($config['binary'] ?? null),
        ], static fn (mixed $value): bool => $value !== null);
        $settings = Settings::resolve($options, $this->laravel->storagePath('runwisp'), $this->laravel->basePath());
        /** @var array<string, mixed> $rest */
        $rest = (array) ($config['config'] ?? []);

        return new Worker($settings, $rest, array_column($units, 0), $log);
    }

    private static function string(mixed $value): ?string
    {
        return is_string($value) && $value !== '' ? $value : null;
    }
}
