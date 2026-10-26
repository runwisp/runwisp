<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\Symfony;

use RunWisp\Settings;
use RunWisp\Worker;
use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\ConsoleOutputInterface;
use Symfony\Component\Console\Output\OutputInterface;

/** @phpstan-import-type Options from Settings */
#[AsCommand('runwisp:work', 'Run the scheduled tasks under RunWisp until stopped')]
class WorkCommand extends Command
{
    /**
     * @param Options              $options
     * @param array<string, mixed> $config  the rest of a runwisp.toml
     */
    public function __construct(
        private readonly Schedules $schedules,
        private readonly array $options,
        private readonly array $config,
        private readonly string $projectDir,
        private readonly string $environment = 'prod',
        private readonly bool $debug = false,
    ) {
        parent::__construct();
    }

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        return $this->worker($output)?->run() ?? self::FAILURE;
    }

    /** The worker for this app's schedules and runwisp config, or null when it can't run tasks. */
    protected function worker(OutputInterface $output): ?Worker
    {
        $log = $output instanceof ConsoleOutputInterface ? $output->getErrorOutput() : $output;
        $say = static function (string $message) use ($log): void {
            $log->writeln($message, OutputInterface::OUTPUT_RAW);
        };
        $console = realpath((string) ($_SERVER['SCRIPT_FILENAME'] ?? ''));
        if ($console === false) {
            $say('runwisp: could not tell which console script runs this command');

            return null;
        }
        // A run gets the worker's kernel environment also when it came from --env, which the env doesn't carry.
        $command = ['env', "APP_ENV={$this->environment}", 'APP_DEBUG=' . (int) $this->debug, PHP_BINARY, $console];
        $units = $this->schedules->units($command, $this->getApplication(), $say);
        $settings = Settings::resolve($this->options, $this->projectDir . '/var/runwisp', $this->projectDir);

        return new Worker($settings, $this->config, $units, static function (string $level, string $message) use ($output, $say): void {
            $level === 'info' ? $output->writeln($message, OutputInterface::OUTPUT_RAW) : $say($message);
        });
    }
}
