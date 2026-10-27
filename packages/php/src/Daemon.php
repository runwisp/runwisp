<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp;

/**
 * Talking to and starting the runwisp daemon for one data directory. Reuses
 * what the runwisp CLI does: the same flags, env vars and local socket API.
 * Mirrors packages/js/src/daemon.ts.
 */
final class Daemon
{
    public function __construct(private readonly Settings $settings)
    {
    }

    /**
     * One HTTP request over the daemon's socket (a trusted local caller, no
     * login). HTTP/1.0, so the answer is never chunked.
     *
     * @return array{status: int, body: mixed}
     */
    public function call(string $method, string $path, float $timeout = 10.0): array
    {
        $conn = @stream_socket_client('unix://' . $this->settings->socket, $errno, $error, $timeout);
        if ($conn === false) {
            throw new \RuntimeException("could not reach RunWisp at {$this->settings->socket}: $error");
        }
        stream_set_timeout($conn, (int) ceil($timeout));
        fwrite($conn, "$method $path HTTP/1.0\r\nHost: runwisp\r\nContent-Length: 0\r\n\r\n");
        $raw = stream_get_contents($conn);
        $timedOut = stream_get_meta_data($conn)['timed_out'];
        fclose($conn);
        if ($timedOut || $raw === false) {
            throw new \RuntimeException("RunWisp did not answer $method $path in time");
        }
        [$head, $body] = array_pad(explode("\r\n\r\n", $raw, 2), 2, '');
        if (preg_match('#^HTTP/1\.[01] (\d{3})#', $head, $m) !== 1) {
            throw new \RuntimeException("RunWisp hung up during $method $path");
        }
        $decoded = json_decode($body, true);

        return ['status' => (int) $m[1], 'body' => $decoded ?? $body];
    }

    public function healthy(): bool
    {
        try {
            return $this->call('GET', '/health', 2.0)['status'] === 200;
        } catch (\RuntimeException) {
            return false;
        }
    }

    /** @return array{pid: int, version: string, configPath: string, configSource: string} */
    public function identity(): array
    {
        $res = $this->call('GET', '/api/daemon/identity');
        $body = $res['body'];
        if ($res['status'] !== 200 || !is_array($body) || !is_int($body['pid'] ?? null)
            || !is_string($body['version'] ?? null) || !is_string($body['configPath'] ?? null)
            || !is_string($body['configSource'] ?? null)) {
            throw new \RuntimeException('RunWisp identity: ' . self::errorMessage($res));
        }

        return $body;
    }

    /** Stops the daemon answering on the socket, if any, and waits up to 10s for it to exit. */
    public function stop(): void
    {
        try {
            $pid = $this->identity()['pid'];
        } catch (\RuntimeException) {
            return;
        }
        self::signal($pid, 15);
        for ($waited = 0; $waited < 10_000 && self::alive($pid); $waited += 100) {
            usleep(100_000);
        }
    }

    /**
     * The web UI port for a daemon about to start: the configured one, else the
     * one this data dir used before while it is free, else the first free port
     * from 9477. A picked port is saved so the URL stays the same across restarts.
     */
    public function choosePort(): int
    {
        if ($this->settings->port !== null) {
            return $this->settings->port;
        }
        $saved = $this->savedPort();
        if ($saved !== null && $this->portFree($saved)) {
            return $saved;
        }
        $port = Settings::DEFAULT_PORT;
        for ($try = $port; $try < $port + 100; $try++) {
            if ($this->portFree($try)) {
                $port = $try;
                break;
            }
        }
        file_put_contents($this->settings->portFile, (string) $port);
        chmod($this->settings->portFile, 0o600);

        return $port;
    }

    /** The port a previous start picked. */
    public function savedPort(): ?int
    {
        $port = (int) @file_get_contents($this->settings->portFile);

        return $port > 0 ? $port : null;
    }

    /**
     * What RunWisp says about the config: its errors and warnings, and each
     * schedule in words with its next firings (`runwisp validate --explain`).
     *
     * @return array{errors: list<string>, warnings: list<string>, schedules: array<string, array<string, mixed>>}
     */
    public function validate(string $document): array
    {
        $doc = Binary::json($this->settings->binary, ['validate', '--app', '--json', '--explain', '--config', $this->settings->config], $document);
        if (!is_array($doc) || !is_array($doc['errors'] ?? null)) {
            throw new \RuntimeException("could not validate with {$this->settings->binary}");
        }
        $messages = static fn (mixed $list): array => array_values(array_filter(array_column((array) $list, 'message'), 'is_string'));
        $schedules = array_filter((array) ($doc['schedules'] ?? []), 'is_array');

        return [
            'errors' => $messages($doc['errors']),
            'warnings' => $messages($doc['warnings'] ?? []),
            'schedules' => array_column($schedules, null, 'task'),
        ];
    }

    /**
     * Starts `runwisp daemon --app` with the config on its stdin, output in
     * <data>/daemon.log, and waits until it answers. It leaves this process's
     * tree and session, so copies of the app share it, it outlives a worker
     * restart and Ctrl-C, and it exits on its own once no copy is connected.
     * Returns the pid it started, which is not the one answering if another
     * copy won a start race.
     */
    public function start(int $port, string $document): int
    {
        $s = $this->settings;
        $args = [$s->binary, 'daemon', '--app', '--config', $s->config, '--data', $s->data,
            '--socket', $s->socket, '--host', $s->host, '--port', (string) $port];
        // sh starts it in the background, prints its pid and exits, so it is
        // reparented to init. A background job's stdin is /dev/null, so the
        // config goes through fd 3. setsid keeps the terminal's Ctrl-C away from
        // it. ponytail: without setsid (macOS), it shares the worker's process group.
        $script = 'exec 3<&0; log=$1; shift; '
            . 'if command -v setsid >/dev/null 2>&1; then setsid "$@" <&3 3<&- >>"$log" 2>&1 & '
            . 'else "$@" <&3 3<&- >>"$log" 2>&1 & fi; echo $!';
        $proc = proc_open(['/bin/sh', '-c', $script, 'sh', $s->log, ...$args], [0 => ['pipe', 'r'], 1 => ['pipe', 'w'], 2 => ['file', $s->log, 'a']], $pipes, null, $this->env());
        if ($proc === false) {
            throw new \RuntimeException('could not run /bin/sh');
        }
        fwrite($pipes[0], $document);
        fclose($pipes[0]);
        $pid = (int) trim((string) stream_get_contents($pipes[1]));
        fclose($pipes[1]);
        proc_close($proc);
        @chmod($s->log, 0o600);

        $deadline = microtime(true) + 15;
        while (microtime(true) < $deadline) {
            if ($this->healthy()) {
                return $pid;
            }
            // The daemon died at startup (a missing binary shows as sh's "not found" in the log).
            if (!self::alive($pid)) {
                break;
            }
            usleep(100_000);
        }
        // Lost a start race to another copy of the app: its daemon is fine to use.
        if ($this->healthy()) {
            return $pid;
        }
        $tail = implode("\n", array_slice(file($s->log, FILE_IGNORE_NEW_LINES) ?: [], -20));

        throw new \RuntimeException("RunWisp did not start (log: {$s->log})\n$tail");
    }

    /** Notes what the daemon with pid was started with, for staleReason. */
    public function recordLaunch(int $pid, string $document): void
    {
        file_put_contents($this->settings->launch, json_encode(['pid' => $pid, 'fingerprint' => $this->fingerprint($document)]));
        chmod($this->settings->launch, 0o600);
    }

    /**
     * Why the running daemon is not what this app would start now, or null
     * when it is: another runwisp version, or for a daemon this data dir's app
     * started, another env or binary path. A daemon outlives worker restarts,
     * so without this a redeploy with a rotated secret or an upgraded runwisp
     * would keep the old one.
     *
     * @param array{pid: int, version: string} $daemon
     */
    public function staleReason(array $daemon, string $document, ?string $version): ?string
    {
        if ($version !== null && $version !== $daemon['version']) {
            return "it runs runwisp {$daemon['version']} and {$this->settings->binary} is $version";
        }
        $launch = json_decode((string) @file_get_contents($this->settings->launch), true);
        if (!is_array($launch) || ($launch['pid'] ?? null) !== $daemon['pid']
            || ($launch['fingerprint'] ?? null) === $this->fingerprint($document)) {
            return null;
        }

        return 'the app\'s environment or runwisp binary path changed since it started';
    }

    /**
     * A hash of what a daemon started now would run with: the binary path and
     * the env it reads. That is the whole env when the config has units that run
     * in the daemon, whose shell sees it all. Else only what RunWisp itself
     * reads: RUNWISP_*, TZ and the ${VAR}s the config names, so a variable that
     * differs per copy doesn't make the daemon stale. Holds no env value.
     */
    public function fingerprint(string $document): string
    {
        preg_match_all('/\$\{([^}:]+)\}/', $document, $m);
        $named = array_flip($m[1]);
        $whole = self::runsInDaemon($document);
        $env = array_filter(
            $this->env(),
            static fn (string $name): bool => $whole || str_starts_with($name, 'RUNWISP_') || $name === 'TZ' || isset($named[$name]),
            ARRAY_FILTER_USE_KEY,
        );
        ksort($env, SORT_STRING);

        return hash('sha256', (string) json_encode([$this->settings->binary, $env]));
    }

    /**
     * The env a daemon this app starts runs with, and so do its shell tasks.
     *
     * @return array<string, string>
     */
    private function env(): array
    {
        // Settings already resolved these from the env; the inherited ones must not override them.
        $env = getenv();
        unset($env['RUNWISP_PASSWORD'], $env['RUNWISP_AUTH']);
        if ($this->settings->password !== null) {
            $env['RUNWISP_PASSWORD'] = $this->settings->password;
        }
        if (!$this->settings->auth) {
            $env['RUNWISP_AUTH'] = 'off';
        }

        return $env;
    }

    /** Whether the document has a task or service that runs in the daemon rather than in the app. */
    private static function runsInDaemon(string $document): bool
    {
        $doc = json_decode($document, true);
        foreach (['tasks', 'services'] as $kind) {
            foreach ((array) ($doc[$kind] ?? []) as $unit) {
                if (($unit['sdk'] ?? false) !== true) {
                    return true;
                }
            }
        }

        return false;
    }

    private function portFree(int $port): bool
    {
        $host = str_contains($this->settings->host, ':') ? "[{$this->settings->host}]" : $this->settings->host;
        $server = @stream_socket_server("tcp://$host:$port");
        if ($server === false) {
            return false;
        }
        fclose($server);

        return true;
    }

    /** @param array{status: int, body: mixed} $res */
    private static function errorMessage(array $res): string
    {
        $body = $res['body'];
        $parts = [is_array($body) && is_string($body['detail'] ?? null) ? $body['detail'] : "HTTP {$res['status']}"];
        foreach (is_array($body) ? (array) ($body['errors'] ?? []) : [] as $error) {
            if (is_string($error['message'] ?? null)) {
                $parts[] = $error['message'];
            }
        }

        return implode(': ', $parts);
    }

    /** Sends a signal to a pid without needing ext-posix. */
    public static function signal(int $pid, int $signal): void
    {
        if (function_exists('posix_kill')) {
            @posix_kill($pid, $signal);
        } else {
            @exec("kill -$signal $pid 2>/dev/null");
        }
    }

    /**
     * Whether pid runs. A zombie counts as gone: with the worker as PID 1
     * (Docker without an init), an exited daemon is reparented to it and never
     * reaped. Where /proc is missing (macOS), kill(0) decides alone.
     */
    public static function alive(int $pid): bool
    {
        if ($pid <= 0) {
            return false;
        }
        if (function_exists('posix_kill')) {
            if (!@posix_kill($pid, 0)) {
                return posix_get_last_error() === 1; // EPERM: it runs, as someone else
            }
        } else {
            exec("kill -0 $pid 2>/dev/null", $_, $code);
            if ($code !== 0) {
                return false;
            }
        }
        $stat = @file_get_contents("/proc/$pid/stat");

        return $stat === false || self::procState($stat) !== 'Z';
    }

    /** The state letter of a /proc/<pid>/stat line: "Z" for a zombie. */
    public static function procState(string $stat): ?string
    {
        // The command name, in parentheses, may itself hold ") ": the state follows the last one.
        return preg_match('/^\) (\S)/', substr($stat, (int) strrpos($stat, ')')), $m) === 1 ? $m[1] : null;
    }
}
