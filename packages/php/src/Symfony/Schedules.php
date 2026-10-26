<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\Symfony;

use RunWisp\Names;
use RunWisp\Task;
use RunWisp\Unit;
use Symfony\Component\Console\Application;
use Symfony\Component\Console\Command\LazyCommand;
use Symfony\Component\Console\Messenger\RunCommandMessage;
use Symfony\Component\Messenger\Envelope;
use Symfony\Component\Messenger\Message\RedispatchMessage;
use Symfony\Component\Scheduler\Generator\MessageContext;
use Symfony\Component\Scheduler\Messenger\ServiceCallMessage;
use Symfony\Component\Scheduler\RecurringMessage;
use Symfony\Component\Scheduler\ScheduleProviderInterface;
use Symfony\Component\Scheduler\Trigger\CronExpressionTrigger;
use Symfony\Component\Scheduler\Trigger\JitterTrigger;
use Symfony\Component\Scheduler\Trigger\PeriodicalTrigger;
use Symfony\Component\Scheduler\Trigger\StaticMessageProvider;
use Symfony\Component\Scheduler\Trigger\TriggerInterface;
use Symfony\Contracts\Service\ServiceProviderInterface;

/** The app's Symfony Scheduler schedules, as RunWisp tasks. */
final class Schedules
{
    /** @param ServiceProviderInterface<ScheduleProviderInterface> $providers schedule providers by schedule name */
    public function __construct(private readonly ServiceProviderInterface $providers)
    {
    }

    /**
     * One task per recurring message RunWisp can schedule.
     *
     * @param list<string>                  $command the console, run again with `runwisp:run <name>` for one run
     * @param ?Application                  $console finds the class of a scheduled command, for its #[Task]
     * @param (\Closure(string): void)|null $warn
     *
     * @return list<Unit>
     */
    public function units(array $command, ?Application $console, ?\Closure $warn = null): array
    {
        $units = [];
        foreach ($this->map($console, $warn) as $name => [, , $options]) {
            $units[] = new Unit($name, $options, [...$command, 'runwisp:run', $name]);
        }

        return $units;
    }

    /**
     * The schedule and recurring message behind a task name.
     *
     * @return array{string, RecurringMessage}|null
     */
    public function find(string $name, ?Application $console): ?array
    {
        $entry = $this->map($console, null)[$name] ?? null;

        return $entry === null ? null : [$entry[0], $entry[1]];
    }

    /**
     * @param (\Closure(string): void)|null $warn
     *
     * @return array<string, array{string, RecurringMessage, array<string, mixed>}>
     */
    private function map(?Application $console, ?\Closure $warn): array
    {
        $names = new Names($warn);
        $entries = [];
        foreach (array_keys($this->providers->getProvidedServices()) as $schedule) {
            $schedule = (string) $schedule;
            foreach ($this->providers->get($schedule)->getSchedule()->getRecurringMessages() as $message) {
                $options = self::options($message->getTrigger());
                if ($options === null) {
                    if ($warn !== null) {
                        $warn("runwisp: skipped a message on schedule \"$schedule\" ({$message->getTrigger()}): RunWisp runs cron and every triggers only");
                    }
                    continue;
                }
                [$class, $method, $label] = self::subject($schedule, $message, $console);
                $task = Task::of($class, $method);
                $name = $names->claim($task->name ?? ($schedule === 'default' ? $label : "$schedule.$label"));
                $entries[$name] = [$schedule, $message, [...$options, ...$task->options ?? []]];
            }
        }

        return $entries;
    }

    /**
     * The [tasks.*] keys for a trigger, or null for one RunWisp can't express.
     *
     * @return array<string, string>|null
     */
    public static function options(TriggerInterface $trigger): ?array
    {
        $jitter = null;
        if ($trigger instanceof JitterTrigger) {
            $jitter = self::field($trigger, 'maxSeconds');
            $trigger = self::field($trigger, 'trigger');
        }
        if ($trigger instanceof CronExpressionTrigger) {
            $options = ['cron' => (string) $trigger];
            $timezone = self::field($trigger, 'timezone');
            if (is_string($timezone)) {
                $options['timezone'] = $timezone;
            }
        } elseif ($trigger instanceof PeriodicalTrigger) {
            // Intervals of months or years have no fixed length: those stay at 0.
            $seconds = self::field($trigger, 'intervalInSeconds');
            if (!is_numeric($seconds) || (int) $seconds < 1) {
                return null;
            }
            $options = ['cron' => '@every ' . (int) $seconds . 's'];
        } else {
            return null;
        }
        if (is_int($jitter) && $jitter > 0) {
            $options['jitter'] = "{$jitter}s";
        }

        return $options;
    }

    /**
     * What the message runs, where a #[Task] on it would be, and the task
     * name made from it: the command, the service method, else the message class.
     *
     * @return array{?string, ?string, string}
     */
    private static function subject(string $schedule, RecurringMessage $message, ?Application $console): array
    {
        $provider = $message->getProvider();
        $subject = null;
        if ($provider instanceof StaticMessageProvider) {
            $context = new MessageContext($schedule, $message->getId(), $message->getTrigger(), new \DateTimeImmutable());
            foreach ($provider->getMessages($context) as $subject) {
                break;
            }
        }
        if ($subject instanceof Envelope) {
            $subject = $subject->getMessage();
        }
        if ($subject instanceof RedispatchMessage) {
            $subject = $subject->envelope instanceof Envelope ? $subject->envelope->getMessage() : $subject->envelope;
        }

        if ($subject instanceof RunCommandMessage) {
            $name = explode(' ', trim($subject->input), 2)[0];

            return [self::commandClass($console, $name), null, $name];
        }

        return match (true) {
            $subject instanceof ServiceCallMessage => [$subject->getServiceId(), $subject->getMethod(), $subject->getServiceId() . '::' . $subject->getMethod()],
            is_object($subject) => [$subject::class, null, (new \ReflectionClass($subject))->getShortName()],
            default => [null, null, $provider->getId()],
        };
    }

    /** The class that holds a console command's code: the command, or an invokable it runs. */
    private static function commandClass(?Application $console, string $name): ?string
    {
        if ($console === null || !$console->has($name)) {
            return null;
        }
        $command = $console->find($name);
        if ($command instanceof LazyCommand) {
            $command = $command->getCommand();
        }
        $code = $command->getCode();
        $owner = match (true) {
            $code instanceof \Closure => (new \ReflectionFunction($code))->getClosureThis(),
            is_array($code) => $code[0],
            default => null,
        } ?? $command;

        return is_object($owner) ? $owner::class : (string) $owner;
    }

    /** A private field of a trigger: Symfony keeps what RunWisp needs to know there. */
    private static function field(object $trigger, string $name): mixed
    {
        try {
            return (new \ReflectionProperty($trigger, $name))->getValue($trigger);
        } catch (\ReflectionException) {
            return null;
        }
    }
}
