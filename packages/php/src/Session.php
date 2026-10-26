<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp;

/**
 * The app's connection to the daemon: `GET /api/local/app` on its socket,
 * upgraded to newline-delimited JSON. The messages mirror
 * apps/runwisp/internal/apphost/host.go.
 */
final class Session
{
    /** The Upgrade token: the app protocol and its version. */
    public const PROTOCOL = 'runwisp-app/1';

    private const TIMEOUT = 10;

    public bool $active = false;
    /** Whether no other copy has runs in RunWisp, as the daemon last said. */
    public bool $alone = false;
    /** The active copy's pid, as told to a standby copy; 0 when unknown. */
    public int $activePid = 0;
    public bool $closed = false;

    /** Bytes read but not yet a whole line. */
    private string $buffer;

    /** @param resource $conn */
    private function __construct(private $conn, string $buffer)
    {
        $this->buffer = $buffer;
        // stream_select sees the socket, not PHP's buffer in front of it.
        stream_set_read_buffer($conn, 0);
    }

    /**
     * Connects and upgrades. The daemon answers with this copy's role first.
     *
     * @param array{pid: int, version: string}|null $running the daemon that answers, for a clearer refusal
     */
    public static function open(string $socket, string $app, int $pid, ?array $running = null): self
    {
        $conn = @stream_socket_client("unix://$socket", $errno, $error, self::TIMEOUT);
        if ($conn === false) {
            throw new \RuntimeException("could not connect to RunWisp at $socket: $error");
        }
        stream_set_timeout($conn, self::TIMEOUT);
        $query = http_build_query(['app' => $app, 'pid' => $pid]);
        self::write($conn, "GET /api/local/app?$query HTTP/1.1\r\nHost: runwisp\r\nConnection: Upgrade\r\nUpgrade: " . self::PROTOCOL . "\r\n\r\n");
        $head = '';
        while (!str_contains($head, "\r\n\r\n")) {
            $chunk = fread($conn, 8192);
            if ($chunk === false || $chunk === '') {
                fclose($conn);

                throw new \RuntimeException('RunWisp closed the app connection');
            }
            $head .= $chunk;
        }
        [$head, $rest] = explode("\r\n\r\n", $head, 2);
        $status = preg_match('#^HTTP/1\.[01] (\d{3})#', $head, $m) === 1 ? $m[1] : '?';
        if ($status !== '101') {
            $length = preg_match('/^content-length:\s*(\d+)/im', $head, $l) === 1 ? (int) $l[1] : 0;
            while (strlen($rest) < $length && ($more = fread($conn, 8192)) !== false && $more !== '') {
                $rest .= $more;
            }
            fclose($conn);

            throw self::refusal($status, $head, $rest, $running);
        }

        return new self($conn, $rest);
    }

    /**
     * Why the daemon refused the app connection, from its HTTP answer.
     *
     * @param array{pid: int, version: string}|null $running
     */
    public static function refusal(string $status, string $head, string $body, ?array $running = null): \RuntimeException
    {
        if ($status === '426') {
            // Usually a daemon an older (or newer) app started, still running.
            $theirs = preg_match('#^upgrade:\s*runwisp-app/(\d+)\s*$#im', $head, $m) === 1 ? $m[1] : 'unknown';
            $daemon = $running !== null ? "RunWisp {$running['version']} (pid {$running['pid']})" : 'The RunWisp running for this data directory';
            $stop = $running !== null ? " (kill {$running['pid']})" : '';

            return new \RuntimeException("$daemon speaks app protocol $theirs, and runwisp/runwisp speaks " . self::PROTOCOL . ". It exits once no copy of the app that started it is left, and this app then starts its own; to switch now, stop it$stop. If this persists, install the same version of runwisp and runwisp/runwisp.");
        }
        $why = trim($body);

        return new \RuntimeException("RunWisp refused the app connection (HTTP $status)" . ($why !== '' ? ": $why" : ''));
    }

    /** @return resource for stream_select */
    public function stream()
    {
        return $this->conn;
    }

    /**
     * The whole messages that arrived, without waiting. Sets closed when the
     * daemon hung up.
     *
     * @return list<array<string, mixed>>
     */
    public function read(): array
    {
        if ($this->closed) {
            return [];
        }
        $read = [$this->conn];
        $none = null;
        while (@stream_select($read, $none, $none, 0) > 0) {
            $chunk = fread($this->conn, 65536);
            if ($chunk === false || $chunk === '') {
                $this->close();
                break;
            }
            $this->buffer .= $chunk;
            $read = [$this->conn];
        }

        return $this->messages();
    }

    /** @param array<string, mixed> $message */
    public function send(array $message): void
    {
        if ($this->closed) {
            return;
        }
        $line = json_encode($message, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE | JSON_INVALID_UTF8_SUBSTITUTE) . "\n";
        if (!self::write($this->conn, $line)) {
            // A write that fails or times out leaves the stream unusable.
            $this->close();
        }
    }

    /** Sends a config document, already JSON, as a reload would apply it. */
    public function config(string $document): void
    {
        if (!$this->closed && !self::write($this->conn, '{"type":"config","doc":' . $document . "}\n")) {
            $this->close();
        }
    }

    public function close(): void
    {
        if (!$this->closed) {
            $this->closed = true;
            fclose($this->conn);
        }
    }

    /** @return list<array<string, mixed>> */
    private function messages(): array
    {
        $out = [];
        while (($end = strpos($this->buffer, "\n")) !== false) {
            $line = substr($this->buffer, 0, $end);
            $this->buffer = substr($this->buffer, $end + 1);
            $message = json_decode($line, true);
            // Lines that aren't an object are skipped, so the daemon can be newer than the app.
            if (is_array($message) && is_string($message['type'] ?? null)) {
                if ($message['type'] === 'role') {
                    $this->active = ($message['active'] ?? false) === true;
                    $this->alone = ($message['alone'] ?? false) === true;
                    $this->activePid = is_int($message['pid'] ?? null) ? $message['pid'] : 0;
                } elseif ($message['type'] === 'alone') {
                    $this->alone = true;
                }
                $out[] = $message;
            }
        }

        return $out;
    }

    /** @param resource $conn */
    private static function write($conn, string $data): bool
    {
        while ($data !== '') {
            $written = @fwrite($conn, $data);
            if ($written === false || $written === 0) {
                return false;
            }
            $data = substr($data, $written);
        }

        return true;
    }
}
