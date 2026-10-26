<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp;

/**
 * One run of a task or service, in the child process the worker started for
 * it. What the process prints is the run's log, and its exit code the run's.
 */
final class Run
{
    /** The env var the worker passes the run in. */
    public const ENV = 'RUNWISP_RUN';

    private static bool $stopping = false;
    private static bool $listening = false;

    /**
     * @param array<string, string> $params every parameter of the run
     * @param array<string, string> $env    the unit's env, secrets and env params, also in the process env
     */
    public function __construct(
        public readonly string $id,
        public readonly string $task,
        public readonly array $params = [],
        public readonly array $env = [],
        /** The firing, for a run the schedule started; null for one started by hand. */
        public readonly ?\DateTimeImmutable $scheduledAt = null,
    ) {
    }

    /** A run started by hand outside RunWisp, as `run <name>` does: no id, no params. */
    public static function manual(string $task): self
    {
        return new self('', $task);
    }

    /** The run this process was started for, or null outside one. */
    public static function current(): ?self
    {
        $payload = json_decode((string) Settings::env(self::ENV), true);
        if (!is_array($payload) || !is_string($payload['run'] ?? null) || !is_string($payload['task'] ?? null)) {
            return null;
        }
        $env = [];
        foreach ((array) ($payload['env'] ?? []) as $name) {
            $value = is_string($name) ? getenv($name) : false;
            if (is_string($value)) {
                $env[$name] = $value;
            }
        }

        return new self(
            $payload['run'],
            $payload['task'],
            array_map('strval', (array) ($payload['params'] ?? [])),
            $env,
            is_string($payload['at'] ?? null) ? new \DateTimeImmutable($payload['at']) : null,
        );
    }

    /**
     * The worker's message for one run, as the child reads it back.
     *
     * @param array<string, string> $env
     * @param array<string, string> $params
     */
    public static function payload(string $run, string $task, array $env, array $params, ?string $at): string
    {
        return (string) json_encode(['run' => $run, 'task' => $task, 'env' => array_keys($env), 'params' => (object) $params, 'at' => $at], JSON_UNESCAPED_SLASHES);
    }

    /**
     * Whether RunWisp asked the run to stop (the web UI, a timeout, shutdown).
     * The first call takes over SIGTERM, so a run that checks this gets its
     * graceful_stop to finish; one that never does ends on SIGTERM right away.
     * Always false without ext-pcntl.
     */
    public function stopping(): bool
    {
        if (!self::$listening && function_exists('pcntl_signal')) {
            self::$listening = true;
            pcntl_async_signals(true);
            pcntl_signal(15, static function (): void {
                self::$stopping = true;
            });
        }

        return self::$stopping;
    }

    /**
     * Calls fn with this run and returns the exit code: 0 when it returns, 1
     * with the error on stderr when it throws. An int fn returns is the code.
     */
    public function execute(callable $fn): int
    {
        try {
            $result = $fn($this);
        } catch (\Throwable $e) {
            fwrite(STDERR, $e . "\n");

            return 1;
        }

        return is_int($result) ? $result : 0;
    }
}
