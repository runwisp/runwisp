<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp;

/**
 * Names a scheduled task and adds RunWisp keys to it, next to the code it
 * runs: on a command, job or message class, on a service method, or on a
 * closure. The keys are [tasks.*] keys as in runwisp.toml:
 *
 *     #[Task('nightly-backup', retry_attempts: 3, timeout: '1h', notify: ['slack'])]
 *
 * The schedule comes from the framework's scheduler, so cron and timezone
 * aren't keys here.
 */
#[\Attribute(\Attribute::TARGET_CLASS | \Attribute::TARGET_METHOD | \Attribute::TARGET_FUNCTION)]
final class Task
{
    private const SCHEDULED = ['cron', 'timezone', 'run', 'sdk'];

    /** @var array<string, mixed> */
    public readonly array $options;

    /** @param ?string $name the task name; default: one made from what it runs */
    public function __construct(public readonly ?string $name = null, mixed ...$options)
    {
        foreach (array_keys($options) as $key) {
            if (!is_string($key)) {
                throw new \InvalidArgumentException('#[RunWisp\Task] takes the name, then named keys such as retry_attempts: 3');
            }
            if (in_array($key, self::SCHEDULED, true)) {
                throw new \InvalidArgumentException("#[RunWisp\\Task] can't set $key: the scheduler does");
            }
        }
        /** @var array<string, mixed> $options */
        $this->options = $options;
    }

    /** The attribute on a method, else on its class, else on a closure. */
    public static function of(\Closure|string|null $target, ?string $method = null): ?self
    {
        try {
            $reflections = match (true) {
                $target instanceof \Closure => [new \ReflectionFunction($target)],
                $target === null || !class_exists($target) => [],
                default => [
                    ...($method !== null && method_exists($target, $method) ? [new \ReflectionMethod($target, $method)] : []),
                    new \ReflectionClass($target),
                ],
            };
        } catch (\ReflectionException) {
            return null;
        }
        foreach ($reflections as $reflection) {
            foreach ($reflection->getAttributes(self::class) as $attribute) {
                return $attribute->newInstance();
            }
        }

        return null;
    }
}
