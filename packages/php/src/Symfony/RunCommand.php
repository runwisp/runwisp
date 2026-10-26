<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\Symfony;

use RunWisp\Run;
use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputArgument;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\StringInput;
use Symfony\Component\Console\Messenger\RunCommandMessage;
use Symfony\Component\Console\Output\ConsoleOutputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Messenger\MessageBusInterface;
use Symfony\Component\Messenger\Stamp\ReceivedStamp;
use Symfony\Component\Scheduler\Generator\MessageContext;
use Symfony\Component\Scheduler\Messenger\ScheduledStamp;

/** One run of a scheduled message, in the process the worker started for it. */
#[AsCommand('runwisp:run', 'Run one scheduled task now, as RunWisp does')]
final class RunCommand extends Command
{
    public function __construct(
        private readonly Schedules $schedules,
        private readonly ?MessageBusInterface $bus = null,
    ) {
        parent::__construct();
    }

    protected function configure(): void
    {
        $this->addArgument('name', InputArgument::REQUIRED, 'The task name, as runwisp:work names it');
    }

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        $name = (string) $input->getArgument('name');
        $found = $this->schedules->find($name, $this->getApplication());
        if ($found === null) {
            $error = $output instanceof ConsoleOutputInterface ? $output->getErrorOutput() : $output;
            $error->writeln("runwisp: this app's schedules don't define \"$name\"", OutputInterface::OUTPUT_RAW);

            return self::FAILURE;
        }
        [$schedule, $recurring] = $found;
        $run = Run::current() ?? Run::manual($name);

        return $run->execute(function (Run $run) use ($schedule, $recurring, $output): int {
            $context = new MessageContext($schedule, $recurring->getId(), $recurring->getTrigger(), $run->scheduledAt ?? new \DateTimeImmutable());
            foreach ($recurring->getMessages($context) as $message) {
                $code = $message instanceof RunCommandMessage
                    ? $this->runCommand($message, $output)
                    : $this->dispatch($message, $schedule, $context);
                if ($code !== self::SUCCESS) {
                    return $code;
                }
            }

            return self::SUCCESS;
        });
    }

    /** Runs the command here, with this output: its message handler would buffer what it prints. */
    private function runCommand(RunCommandMessage $message, OutputInterface $output): int
    {
        $application = $this->getApplication() ?? throw new \LogicException('runwisp:run runs outside a console application');
        $application->setAutoExit(false);
        // A thrown error fails the run with its trace, as Run::execute reports it.
        $application->setCatchExceptions($message->catchExceptions);

        return $application->run(new StringInput($message->input), $output);
    }

    /** Handles the message here and now, as the scheduler transport's consumer would. */
    private function dispatch(object $message, string $schedule, MessageContext $context): int
    {
        if ($this->bus === null) {
            throw new \LogicException('running a scheduled message needs symfony/messenger');
        }
        $this->bus->dispatch($message, [new ReceivedStamp("scheduler_$schedule"), new ScheduledStamp($context)]);

        return self::SUCCESS;
    }
}
