<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp;

use Symfony\Component\Process\Exception\ProcessTimedOutException;
use Symfony\Component\Process\Process;

/**
 * The long-running side of the app: starts RunWisp with the app's tasks as its
 * config, stays connected so RunWisp keeps running, and runs each task or
 * service RunWisp sends in its own child process, relaying what it prints and
 * how it exits. Several copies may run: the first is active, the others stand
 * by and take over when it stops.
 */
final class Worker
{
    private const TICK_US = 100_000;
    private const RECONNECT_S = 1;
    private const MAX_RECONNECT_S = 30;
    /** Set on the copy a reload starts: it writes this app's tasks to that file and exits. */
    private const DESCRIBE = 'RUNWISP_DESCRIBE';
    /** How long the last copy to stop waits for RunWisp to stop with it. */
    private const DAEMON_STOP_S = 30;
    /** Under the 25 seconds RunWisp waits (apphost.ReloadTimeout), so RunWisp hears why. */
    private const RELOAD_TIMEOUT_S = 20;
    /** How much of a failed reload's output RunWisp is told. */
    private const RELOAD_ERROR_LINES = 20;

    private readonly Daemon $daemon;
    /** @var array<string, Unit> */
    private array $units = [];
    /** @var \Closure(string, string): void */
    private readonly \Closure $log;
    private readonly ?string $setsid;

    private ?Session $session = null;
    /** @var array<string, array{process: Process, session: Session, stdout: Lines, stderr: Lines, group: bool}> */
    private array $runs = [];
    /** Why the daemon is not what this copy would start now; null when it is. */
    private ?string $stale = null;
    /** The document RunWisp applied from this copy, or the one waiting for its answer. */
    private ?string $pushed = null;
    /**
     * The configs sent and waiting for RunWisp's answer, oldest first: what a
     * reload read, to take on once RunWisp accepts it, or null for this copy's
     * own config.
     *
     * @var list<array{array<string, mixed>, array<string, Unit>}|null>
     */
    private array $pending = [];
    private bool $standby = false;
    private bool $announced = false;
    /** @var array<string, true> */
    private array $warned = [];
    /** @var array<string, true> */
    private array $configWarnings = [];
    private int $retries = 0;
    private float $retryAt = 0;
    /** The signal that asked the worker to stop, once one did. */
    private ?int $leaving = null;
    /** The RunWisp daemon this copy is connected to. */
    private int $daemonPid = 0;
    /** How this worker was started, for a reload to read the app's tasks as they are now. */
    private readonly string $cwd;
    /** @var list<string> */
    private readonly array $argv;
    /** @var array{process: Process, file: string, ids: list<string>, session: Session}|null the reload under way */
    private ?array $reloading = null;

    /**
     * @param array<string, mixed>                  $config the rest of a runwisp.toml: notifiers, defaults, shell tasks
     * @param list<Unit>                            $units
     * @param (\Closure(string, string): void)|null $log    level (info, warn, error) and message; default: info to stdout, the rest to stderr
     */
    public function __construct(
        private readonly Settings $settings,
        private array $config,
        array $units,
        ?\Closure $log = null,
    ) {
        $this->daemon = new Daemon($settings);
        foreach ($units as $unit) {
            $this->units[$unit->name] = $unit;
        }
        $this->log = $log ?? static function (string $level, string $message): void {
            fwrite($level === 'info' ? STDOUT : STDERR, $message . "\n");
        };
        $this->setsid = self::which('setsid');
        $this->cwd = (string) getcwd();
        $this->argv = array_values(array_map('strval', (array) ($_SERVER['argv'] ?? [])));
    }

    /**
     * The whole config this app gives RunWisp, as JSON.
     *
     * @throws \RuntimeException when the config also defines a task or service the code does
     */
    public function document(): string
    {
        return self::documentOf($this->config, $this->units);
    }

    /**
     * @param array<string, mixed> $config
     * @param array<string, Unit>  $units
     */
    private static function documentOf(array $config, array $units): string
    {
        $doc = $config;
        $tasks = (array) ($doc['tasks'] ?? []);
        $services = (array) ($doc['services'] ?? []);
        foreach ($units as $name => $unit) {
            $kind = $unit->service ? 'services' : 'tasks';
            if (isset($tasks[$name]) || isset($services[$name])) {
                throw new \RuntimeException("the config's $kind also define \"$name\", which the code defines; set its keys in the code, with #[RunWisp\\Task] for a scheduled task");
            }
            if ($unit->service) {
                $services[$name] = [...$unit->options, 'sdk' => true];
            } else {
                $tasks[$name] = [...$unit->options, 'sdk' => true];
            }
        }
        $doc['tasks'] = (object) $tasks;
        $doc['services'] = (object) $services;

        return json_encode($doc, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE | JSON_THROW_ON_ERROR);
    }

    /**
     * Prints each task and service with its schedule, next run and options,
     * and whether RunWisp accepts the config, without starting anything.
     * Returns 0 when it does, 1 when not.
     */
    public function check(): int
    {
        $report = $this->validated();
        if ($report === null) {
            return 1;
        }
        /** @var array{tasks: array<string, array<string, mixed>>, services: array<string, array<string, mixed>>} $doc */
        $doc = json_decode($report['document'], true, 512, JSON_THROW_ON_ERROR);
        Check::print($doc, $report['schedules'], $this->log);

        return 0;
    }

    /**
     * The config document and what RunWisp says about it, with its warnings
     * logged; null, with the reason logged, when it is rejected.
     *
     * @return array{document: string, schedules: array<string, array<string, mixed>>}|null
     */
    private function validated(): ?array
    {
        try {
            $document = $this->document();
            $report = $this->daemon->validate($document);
        } catch (\RuntimeException $e) {
            ($this->log)('error', "runwisp: {$e->getMessage()}");

            return null;
        }
        foreach ($report['warnings'] as $warning) {
            ($this->log)('warn', "runwisp: $warning");
        }
        // The daemon repeats them when it applies the config: already said.
        $this->configWarnings = array_fill_keys($report['warnings'], true);
        if ($report['errors'] !== []) {
            ($this->log)('error', 'runwisp: RunWisp rejects the config:');
            foreach ($report['errors'] as $error) {
                ($this->log)('error', '  ' . str_replace("\n", "\n  ", $error));
            }

            return null;
        }

        return ['document' => $document, 'schedules' => $report['schedules']];
    }

    /**
     * Runs until SIGTERM or SIGINT (with ext-pcntl), then stops taking runs,
     * lets the running ones finish, and returns 128 + the signal. A second
     * signal kills them and returns at once.
     */
    public function run(): int
    {
        $describe = getenv(self::DESCRIBE);
        if (is_string($describe) && $describe !== '') {
            return $this->describe($describe);
        }
        // Checked first: an invalid config is the app's to fix, not a reason to retry.
        if ($this->validated() === null) {
            return 1;
        }
        $this->listenForSignals();
        while (true) {
            if ($this->leaving !== null && ($this->runs === [] || $this->session === null)) {
                $this->killAll(15);
                $this->stop();

                return 128 + $this->leaving;
            }
            if ($this->session === null && $this->leaving === null && microtime(true) >= $this->retryAt) {
                $this->connect();
            }
            $this->tick();
        }
    }

    /**
     * Asks the running worker, through RunWisp, to read this app's tasks
     * again: what to run after a deploy. Prints what changed, and returns
     * non-zero when RunWisp or the app rejected it, which leaves the running
     * tasks as they were.
     */
    public function reload(): int
    {
        $process = new Process([$this->settings->binary, 'reload', '--data', $this->settings->data, '--socket', $this->settings->socket], null, null, null, null);
        try {
            $process->run();
        } catch (\Throwable $e) {
            ($this->log)('error', "runwisp: could not run {$this->settings->binary}: {$e->getMessage()}");

            return 1;
        }
        foreach (['info' => $process->getOutput(), 'error' => $process->getErrorOutput()] as $level => $text) {
            if (trim($text) !== '') {
                ($this->log)($level, rtrim($text));
            }
        }

        return $process->getExitCode() ?? 1;
    }

    /** Writes the tasks and config this copy of the app defines to $path, for the worker that started it to reload. */
    private function describe(string $path): int
    {
        $units = array_map(static fn (Unit $unit): array => [
            'name' => $unit->name,
            'options' => $unit->options,
            'argv' => $unit->argv,
            'service' => $unit->service,
        ], array_values($this->units));
        file_put_contents($path, json_encode(['config' => $this->config, 'units' => $units], JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE | JSON_THROW_ON_ERROR));

        return 0;
    }

    /**
     * RunWisp asks for the config as the code defines it now. This process
     * loaded the code as it was when it started, so a new one reads it: the
     * same command this worker was started with, which writes its tasks out
     * instead of running them.
     */
    private function startReload(Session $session, string $id): void
    {
        if ($this->reloading !== null) {
            $this->reloading['ids'][] = $id;

            return;
        }
        $file = "{$this->settings->data}/reload.json";
        @unlink($file);
        $process = new Process([PHP_BINARY, ...$this->argv], $this->cwd, [self::DESCRIBE => $file], null, self::RELOAD_TIMEOUT_S);
        try {
            $process->start();
        } catch (\Throwable $e) {
            $session->send(['type' => 'reload_error', 'reload' => $id, 'error' => "could not start PHP to read the tasks: {$e->getMessage()}"]);

            return;
        }
        $this->reloading = ['process' => $process, 'file' => $file, 'ids' => [$id], 'session' => $session];
    }

    /** Once the reload's process ended: takes on what it read and sends it to RunWisp, or tells RunWisp why it can't. */
    private function finishReload(): void
    {
        $reload = $this->reloading;
        if ($reload === null) {
            return;
        }
        $process = $reload['process'];
        try {
            $process->checkTimeout();
        } catch (ProcessTimedOutException) {
            // Stopped: reported below like any other failure.
        }
        if ($process->isRunning()) {
            return;
        }
        $this->reloading = null;
        $error = null;
        $read = null;
        try {
            $read = $this->read($process, $reload['file']);
            $document = self::documentOf(...$read);
        } catch (\RuntimeException $e) {
            $document = null;
            $error = $e->getMessage();
        } finally {
            @unlink($reload['file']);
        }
        $session = $reload['session'];
        if ($session !== $this->session) {
            return; // RunWisp hung up meanwhile, which failed the reload: the tasks stay.
        }
        foreach ($reload['ids'] as $id) {
            if ($document === null) {
                $session->send(['type' => 'reload_error', 'reload' => $id, 'error' => $error ?? '']);
                continue;
            }
            // Taken on once RunWisp accepts it: a rejected reload leaves the tasks as they were.
            $this->pending[] = $read;
            $this->pushed = $document;
            $session->config($document, $id);
        }
    }

    /**
     * What a reload's process read.
     *
     * @return array{array<string, mixed>, array<string, Unit>}
     *
     * @throws \RuntimeException with the process's own output when it failed
     */
    private function read(Process $process, string $file): array
    {
        $json = $process->isSuccessful() ? @file_get_contents($file) : false;
        $data = is_string($json) ? json_decode($json, true) : null;
        if (!is_array($data) || !is_array($data['units'] ?? null)) {
            $lines = preg_split('/\R/', trim($process->getErrorOutput() . "\n" . $process->getOutput())) ?: [];
            $tail = implode("\n", array_slice(array_values(array_filter($lines, static fn (string $l): bool => trim($l) !== '')), -self::RELOAD_ERROR_LINES));
            $why = $process->isSuccessful() ? 'it wrote no tasks' : 'exit code ' . ($process->getExitCode() ?? 'none, it was stopped after ' . self::RELOAD_TIMEOUT_S . 's');
            throw new \RuntimeException("reading the tasks with `php " . implode(' ', $this->argv) . "` failed ($why)" . ($tail === '' ? '' : ":\n$tail"));
        }
        $units = [];
        foreach ($data['units'] as $unit) {
            $unit = (array) $unit;
            $name = (string) ($unit['name'] ?? '');
            $units[$name] = new Unit($name, (array) ($unit['options'] ?? []), array_values(array_map('strval', (array) ($unit['argv'] ?? []))), (bool) ($unit['service'] ?? false));
        }

        return [(array) ($data['config'] ?? []), $units];
    }

    /**
     * Disconnects for good. The last copy to stop waits for RunWisp to stop
     * too, which it does as this copy goes: under systemd's KillMode=mixed,
     * whatever outlives the worker is killed, and a RunWisp killed before it
     * saved how the last runs ended records them as crashed.
     */
    private function stop(): void
    {
        $session = $this->session;
        if ($session === null) {
            return;
        }
        // The leave is answered at once when another copy takes over: wait a tick for that.
        $read = [$session->stream()];
        $none = null;
        @stream_select($read, $none, $none, 0, self::TICK_US);
        foreach ($session->read() as $message) {
            $this->receive($session, $message);
        }
        $last = $session->active;
        $session->close();
        for ($deadline = microtime(true) + self::DAEMON_STOP_S; $last && Daemon::alive($this->daemonPid) && microtime(true) < $deadline;) {
            usleep(self::TICK_US);
        }
    }

    /** One turn: wait up to a tick for RunWisp, handle what it sent, relay run output. */
    public function tick(): void
    {
        $session = $this->session;
        if ($session !== null) {
            $read = [$session->stream()];
            $none = null;
            @stream_select($read, $none, $none, 0, self::TICK_US);
            foreach ($session->read() as $message) {
                $this->receive($session, $message);
            }
            if ($session->closed && $this->session === $session) {
                $this->disconnected();
            }
        } else {
            usleep(self::TICK_US);
        }
        $this->pump();
        $this->finishReload();
    }

    private function connect(): void
    {
        try {
            $this->attach();
        } catch (\Throwable $e) {
            $delay = min(self::RECONNECT_S * 2 ** $this->retries, self::MAX_RECONNECT_S);
            // A repeat failure gets its first line only: the first one had the details.
            $text = $this->retries === 0 ? $e->getMessage() : strtok($e->getMessage(), "\n");
            ($this->log)('error', "runwisp: $text (retrying in {$delay}s)");
            $this->retries++;
            $this->retryAt = microtime(true) + $delay;
        }
    }

    /** Starts RunWisp with this app's config unless another copy already did, and connects to it. */
    private function attach(): void
    {
        $this->settings->prepareDirs();
        $document = $this->document();
        $started = null;
        if (!$this->daemon->healthy()) {
            $started = $this->daemon->start($this->daemon->choosePort(), $document);
        }
        $daemon = $this->daemon->identity();
        $version = Binary::version($this->settings->binary);
        $mismatch = Binary::mismatch($this->settings->binary, $version);
        if ($mismatch !== null) {
            $this->warnOnce('version', $mismatch);
        }
        if ($daemon['configSource'] !== 'app') {
            throw new \RuntimeException("the RunWisp daemon on {$this->settings->data} runs {$daemon['configPath']}, not this app's tasks; pass a different data directory");
        }
        if ($started === $daemon['pid']) {
            $this->daemon->recordLaunch($daemon['pid'], $document);
            $this->stale = null;
        } else {
            $this->stale = $this->daemon->staleReason($daemon, $document, $version);
        }
        $this->daemonPid = (int) $daemon['pid'];
        $this->session = Session::open($this->settings->socket, $this->settings->app, getmypid() ?: 0, $daemon);
        $this->pushed = null;
        $this->pending = [];
        $this->retries = 0;
        $this->announce();
    }

    /** @param array<string, mixed> $m */
    private function receive(Session $session, array $m): void
    {
        switch ($m['type']) {
            case 'role':
                $this->role($session);
                break;
            case 'alone':
                $this->apply();
                break;
            case 'run':
                $this->start($session, $m);
                break;
            case 'reload':
                $this->startReload($session, (string) ($m['reload'] ?? ''));
                break;
            case 'stop':
                $this->signalRun((string) ($m['run'] ?? ''), 15);
                break;
            case 'kill':
                // RunWisp gave up on the run after its graceful_stop.
                $this->signalRun((string) ($m['run'] ?? ''), 9);
                break;
            case 'config_ok':
                $read = array_shift($this->pending);
                if ($read !== null) {
                    [$this->config, $this->units] = $read;
                }
                $warnings = array_filter((array) ($m['result']['warnings'] ?? []), 'is_string');
                foreach ($warnings as $warning) {
                    if (!isset($this->configWarnings[$warning])) {
                        ($this->log)('warn', "runwisp: $warning");
                    }
                }
                $this->configWarnings = array_fill_keys($warnings, true);
                break;
            case 'config_error':
                $reload = array_shift($this->pending) !== null;
                $this->pushed = null;
                $error = is_string($m['error'] ?? null) ? $m['error'] : 'RunWisp rejected the config';
                if ($reload) {
                    // The reload's caller hears it too. Even a change that needs a restart
                    // doesn't get one here: that would stop the runs in progress.
                    ($this->log)('error', "runwisp: reload rejected, the tasks stay as they were: $error");
                } elseif (($m['restart'] ?? false) === true) {
                    $this->restart("to apply the new config ($error)");
                } else {
                    // Most likely an old daemon not knowing the new config: it restarts once alone.
                    $stale = $this->stale === null ? '' : " (RunWisp is stale, {$this->stale}; it restarts with this config once no other copy is still finishing runs)";
                    ($this->log)('error', "runwisp: $error$stale");
                }
                break;
            default:
                // overlap and missed are for node-cron's events; unknown types
                // are ignored, so the daemon can be newer than the app.
        }
    }

    private function role(Session $session): void
    {
        if (!$session->active) {
            if (!$this->standby && $this->leaving === null) {
                $this->standby = true;
                ($this->log)('info', "runwisp: standby: another copy of this app is active (pid {$session->activePid}); this one runs the tasks once that one stops");
            }

            return;
        }
        if ($this->standby) {
            $this->standby = false;
            ($this->log)('info', 'runwisp: this copy of the app is now active and runs the tasks');
        }
        if ($this->stale !== null && !$session->alone) {
            $this->warnOnce('stale', "runwisp: RunWisp is stale ({$this->stale}); it restarts once this copy runs the tasks and no other copy is still finishing runs");
        }
        // A copy that becomes active supplies its own config.
        $this->pushed = null;
        $this->apply();
    }

    /** The active copy restarts a stale RunWisp once alone, else pushes its config. */
    private function apply(): void
    {
        $session = $this->session;
        if ($session === null || !$session->active || $this->leaving !== null) {
            return;
        }
        if ($session->alone && $this->stale !== null) {
            $why = $this->stale;
            $this->stale = null;
            $this->restart("since $why");

            return;
        }
        $document = $this->document();
        if ($this->pending === [] && $document !== $this->pushed) {
            $this->pending[] = null;
            $this->pushed = $document;
            $session->config($document);
        }
    }

    /** A change only a daemon restart applies: restart it with this copy's config and env. */
    private function restart(string $why): void
    {
        ($this->log)('warn', "runwisp: restarting RunWisp $why");
        // Disconnect first: if the daemon doesn't stop, attach() must not meet our own old connection.
        $this->session?->close();
        $this->disconnected();
        $this->daemon->stop();
        $this->retryAt = 0;
    }

    /** RunWisp hung up: its runs here already failed there, so they stop. */
    private function disconnected(): void
    {
        $session = $this->session;
        $this->session = null;
        foreach ($this->runs as $id => $run) {
            if ($run['session'] === $session) {
                $this->signalRun($id, 15);
            }
        }
        $this->retries = 0;
        $this->retryAt = microtime(true) + self::RECONNECT_S;
    }

    /** @param array<string, mixed> $m */
    private function start(Session $session, array $m): void
    {
        $id = (string) ($m['run'] ?? '');
        $name = (string) ($m['task'] ?? '');
        $unit = $this->units[$name] ?? null;
        if ($unit === null) {
            $session->send(['type' => 'output', 'run' => $id, 'stream' => 'stderr', 'line' => "runwisp: this copy of the app doesn't define \"$name\""]);
            $session->send(['type' => 'exit', 'run' => $id, 'code' => 1]);

            return;
        }
        $env = array_map('strval', (array) ($m['env'] ?? []));
        $params = array_map('strval', (array) ($m['params'] ?? []));
        $at = is_string($m['at'] ?? null) ? $m['at'] : null;
        // In its own session, so the terminal's Ctrl-C reaches the worker only
        // (it lets runs finish), and a stop reaches what the run started too.
        $group = $this->setsid !== null && function_exists('posix_kill');
        $argv = $group ? [$this->setsid, ...$unit->argv] : $unit->argv;
        $process = new Process($argv, null, [...$env, Run::ENV => Run::payload($id, $name, $env, $params, $at)], null, null);
        try {
            $process->start();
        } catch (\Throwable $e) {
            $session->send(['type' => 'output', 'run' => $id, 'stream' => 'stderr', 'line' => "runwisp: could not start the run: {$e->getMessage()}"]);
            $session->send(['type' => 'exit', 'run' => $id, 'code' => 1]);

            return;
        }
        $this->runs[$id] = ['process' => $process, 'session' => $session, 'stdout' => new Lines(), 'stderr' => new Lines(), 'group' => $group];
    }

    /** Relays what each run printed, and reports the ones that ended. */
    private function pump(): void
    {
        foreach ($this->runs as $id => $run) {
            $process = $run['process'];
            $running = $process->isRunning();
            $this->relay($id, $run, 'stdout', $run['stdout']->push($process->getIncrementalOutput()));
            $this->relay($id, $run, 'stderr', $run['stderr']->push($process->getIncrementalErrorOutput()));
            if ($running) {
                continue;
            }
            $this->relay($id, $run, 'stdout', $run['stdout']->flush());
            $this->relay($id, $run, 'stderr', $run['stderr']->flush());
            $process->clearOutput()->clearErrorOutput();
            // Killed by a signal: 128 + the signal, as the shell reports it.
            $run['session']->send(['type' => 'exit', 'run' => $id, 'code' => $process->getExitCode() ?? -1]);
            unset($this->runs[$id]);
        }
    }

    /**
     * @param array{session: Session} $run
     * @param list<array{string, bool}> $lines
     */
    private function relay(string $id, array $run, string $stream, array $lines): void
    {
        foreach ($lines as [$line, $partial]) {
            $message = ['type' => 'output', 'run' => $id, 'stream' => $stream, 'line' => $line];
            if ($partial) {
                $message['partial'] = true;
            }
            $run['session']->send($message);
        }
    }

    private function signalRun(string $id, int $signal): void
    {
        $run = $this->runs[$id] ?? null;
        if ($run === null || !$run['process']->isRunning()) {
            return;
        }
        $pid = $run['process']->getPid();
        if ($run['group'] && $pid !== null) {
            // The run leads its own process group: everything it started gets the signal too.
            Daemon::signal(-$pid, $signal);
        } else {
            // ponytail: without setsid, processes the run started outlive a kill.
            try {
                $run['process']->signal($signal);
            } catch (\Throwable) {
                // It ended meanwhile.
            }
        }
    }

    private function killAll(int $signal): void
    {
        foreach (array_keys($this->runs) as $id) {
            $this->signalRun((string) $id, $signal);
        }
    }

    private function listenForSignals(): void
    {
        if (!function_exists('pcntl_signal')) {
            return;
        }
        pcntl_async_signals(true);
        foreach ([15, 2] as $signal) {
            pcntl_signal($signal, function (int $signal): void {
                if ($this->leaving !== null) {
                    // A second signal: don't wait for the runs.
                    $this->killAll(9);
                    exit(128 + $signal);
                }
                $this->leaving = $signal;
                // Shutting down for good: the next copy takes over, or RunWisp
                // stops once this one disconnects, after its runs end.
                $this->session?->send(['type' => 'leave']);
            });
        }
    }

    /** Never prints the password itself: worker output ends up in log collectors. */
    private function announce(): void
    {
        if ($this->announced) {
            return;
        }
        $this->announced = true;
        $suffix = '';
        if (!$this->settings->auth) {
            $suffix = ' (login off)';
        } elseif ($this->settings->password === null) {
            $suffix = ', password: ' . $this->passwordCommand();
        }
        ($this->log)('info', 'RunWisp dashboard: ' . $this->settings->url($this->settings->port ?? $this->daemon->savedPort()) . $suffix);
    }

    /** The CLI command that prints RunWisp's per-start password. */
    private function passwordCommand(): string
    {
        $cwd = (string) getcwd();
        $runwisp = is_file("$cwd/vendor/bin/runwisp") ? 'vendor/bin/runwisp' : escapeshellarg($this->settings->binary);
        $data = $this->settings->data;
        if ($this->settings->socket !== "$data/runwisp.sock") {
            return "$runwisp password --socket " . escapeshellarg($this->settings->socket);
        }
        $shown = str_starts_with($data, "$cwd/") ? substr($data, strlen($cwd) + 1) : $data;

        return "$runwisp password --data " . escapeshellarg($shown);
    }

    private function warnOnce(string $key, string $text): void
    {
        if (!isset($this->warned[$key])) {
            $this->warned[$key] = true;
            ($this->log)('warn', $text);
        }
    }

    private static function which(string $command): ?string
    {
        foreach (explode(PATH_SEPARATOR, (string) getenv('PATH')) as $dir) {
            if ($dir !== '' && is_executable("$dir/$command")) {
                return "$dir/$command";
            }
        }

        return null;
    }
}
