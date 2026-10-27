<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp;

/**
 * Where the daemon lives and how it is reached, resolved from the options and
 * the same RUNWISP_* env vars the runwisp CLI reads. Mirrors
 * packages/js/src/daemon.ts.
 *
 * @phpstan-type Options array{app?: string, data?: string, port?: int, host?: string, password?: string, auth?: bool, binary?: string}
 */
final class Settings
{
    public const DEFAULT_PORT = 9477;

    /** The keys of Options, which every integration takes from its own config. */
    public const OPTIONS = ['app', 'data', 'port', 'host', 'password', 'auth', 'binary'];

    // AF_UNIX paths are capped at 104 bytes on macOS (108 on Linux).
    private const MAX_SOCKET_PATH = 100;

    /** Where runwisp.toml would be: relative paths in the config resolve against its directory. Never read. */
    public readonly string $config;
    public readonly string $socket;
    public readonly string $log;
    /** The port a previous start picked. */
    public readonly string $portFile;
    /** What the daemon this app started was started with (pid and fingerprint). */
    public readonly string $launch;

    /**
     * @param string   $app  copies of one app share a daemon; a different app is refused
     * @param int|null $port null: RunWisp picks a free one and keeps it
     */
    public function __construct(
        public readonly string $app,
        public readonly string $data,
        public readonly ?int $port,
        public readonly string $host,
        public readonly ?string $password,
        public readonly bool $auth,
        public readonly string $binary,
        string $cwd,
    ) {
        $this->config = $cwd . '/runwisp.toml';
        $this->socket = self::socketPath($data);
        $this->log = $data . '/daemon.log';
        $this->portFile = $data . '/app.port';
        $this->launch = $data . '/app.launch';
    }

    /**
     * @param Options $options
     * @param string  $defaultData the framework's own place for it; RUNWISP_DATA still wins
     */
    public static function resolve(array $options, ?string $defaultData = null, ?string $cwd = null): self
    {
        $cwd ??= (string) getcwd();
        $envPort = (int) self::env('RUNWISP_PORT');
        // Options win over the env: a password option turns the login on even with RUNWISP_AUTH=off.
        $auth = $options['auth'] ?? (isset($options['password']) || self::env('RUNWISP_AUTH') !== 'off');
        $password = $options['password'] ?? ($auth ? self::env('RUNWISP_PASSWORD') : null);
        if (!$auth && $password !== null) {
            throw new \InvalidArgumentException('password and auth: false can\'t be used together');
        }
        $data = $options['data'] ?? self::env('RUNWISP_DATA') ?? $defaultData
            ?? (function_exists('posix_getuid') && posix_getuid() === 0 ? '/var/lib/runwisp' : '.runwisp');

        return new self(
            app: $options['app'] ?? self::defaultAppName($cwd),
            data: self::absolute($data, $cwd),
            port: $options['port'] ?? ($envPort > 0 ? $envPort : null),
            host: $options['host'] ?? self::env('RUNWISP_HOST') ?? '127.0.0.1',
            password: $password,
            auth: $auth,
            binary: Binary::resolve($options['binary'] ?? null),
            cwd: $cwd,
        );
    }

    /** An env var, treating empty as unset like the runwisp CLI does. */
    public static function env(string $name): ?string
    {
        $value = getenv($name);

        return $value === false || $value === '' ? null : $value;
    }

    /**
     * The `name` in the nearest composer.json from $dir up, else $dir itself.
     * Copies of one app agree on it, wherever each is deployed.
     */
    public static function defaultAppName(string $dir): string
    {
        for ($at = $dir; ; $at = dirname($at)) {
            $file = $at . '/composer.json';
            if (is_file($file)) {
                $name = json_decode((string) file_get_contents($file), true)['name'] ?? null;
                if (is_string($name) && $name !== '') {
                    return $name;
                }
            }
            if (dirname($at) === $at) {
                return $dir;
            }
        }
    }

    public function url(?int $port = null): string
    {
        $host = in_array($this->host, ['0.0.0.0', '::'], true) ? '127.0.0.1' : $this->host;
        if (str_contains($host, ':')) {
            $host = "[$host]";
        }

        return "http://$host:" . ($port ?? $this->port ?? self::DEFAULT_PORT);
    }

    /** Creates the data dir, and the private socket dir when the socket path was too long for it. */
    public function prepareDirs(): void
    {
        if (!is_dir($this->data) && !@mkdir($this->data, 0o700, true) && !is_dir($this->data)) {
            throw new \RuntimeException("could not create {$this->data}");
        }
        $dir = dirname($this->socket);
        if ($dir === $this->data) {
            return;
        }
        if (!is_dir($dir)) {
            @mkdir($dir, 0o700, true);
        }
        $stat = @lstat($dir);
        $uid = function_exists('posix_getuid') ? posix_getuid() : null;
        if ($stat === false || is_link($dir) || !is_dir($dir) || ($uid !== null && $stat['uid'] !== $uid) || ($stat['mode'] & 0o077) !== 0) {
            throw new \RuntimeException("$dir must be a directory that only you can access (mode 0700)");
        }
    }

    private static function socketPath(string $data): string
    {
        $inData = $data . '/runwisp.sock';
        if (strlen($inData) <= self::MAX_SOCKET_PATH) {
            return $inData;
        }
        $hash = substr(hash('sha256', $data), 0, 12);
        $uid = function_exists('posix_getuid') ? posix_getuid() : 0;

        // A per-user directory: a shared /tmp would let another user bind the path first.
        return rtrim(sys_get_temp_dir(), '/') . "/runwisp-$uid/daemon-$hash.sock";
    }

    private static function absolute(string $path, string $cwd): string
    {
        $path = str_starts_with($path, '/') ? $path : $cwd . '/' . $path;
        // Collapse ./ and ../ without touching the filesystem: the dir may not exist yet.
        $parts = [];
        foreach (explode('/', $path) as $part) {
            if ($part === '' || $part === '.') {
                continue;
            }
            if ($part === '..') {
                array_pop($parts);
            } else {
                $parts[] = $part;
            }
        }

        return '/' . implode('/', $parts);
    }
}
