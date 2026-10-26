<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp;

use Composer\InstalledVersions;
use Symfony\Component\Process\Process;

/** The runwisp binary: finding it and asking it things. */
final class Binary
{
    /** Where the Composer plugin puts the binary it downloads. */
    public const DOWNLOADED = __DIR__ . '/../bin/runwisp-bin';

    public const INSTALL_HINT = 'install it with `curl -fsSL https://get.runwisp.com | sh`, or pass its path as the binary option';

    /** The given path, else RUNWISP_BINARY, else the one the Composer plugin downloaded, else `runwisp` on PATH. */
    public static function resolve(?string $explicit = null): string
    {
        $explicit = $explicit !== null && $explicit !== '' ? $explicit : Settings::env('RUNWISP_BINARY');
        if ($explicit !== null) {
            return $explicit;
        }
        $downloaded = realpath(self::DOWNLOADED);

        return $downloaded !== false && is_executable($downloaded) ? $downloaded : 'runwisp';
    }

    /** The version `$binary --version` reports, or null if it can't say. */
    public static function version(string $binary): ?string
    {
        try {
            $process = new Process([$binary, '--version']);
            $process->run();
        } catch (\Throwable) {
            return null;
        }

        return preg_match('/version (\S+)/', $process->getOutput(), $m) === 1 ? $m[1] : null;
    }

    /**
     * A warning when the binary (at version) is not the runwisp this package
     * was released with: only another app protocol keeps them from working
     * together, so this is the one sign of a binary left behind by an upgrade.
     * Dev builds never warn.
     */
    public static function mismatch(string $binary, ?string $version, ?string $sdk = null): ?string
    {
        $sdk ??= self::sdkVersion();
        $unknown = static fn (?string $v): bool => $v === null || $v === '0.0.0' || str_contains($v, 'dev');
        if ($unknown($version) || $unknown($sdk) || $version === $sdk) {
            return null;
        }

        return "runwisp: $binary is runwisp $version and runwisp/runwisp is $sdk: install the same version of both";
    }

    /** This package's version without the v, or null when Composer can't say. */
    public static function sdkVersion(): ?string
    {
        if (!class_exists(InstalledVersions::class) || !InstalledVersions::isInstalled('runwisp/runwisp')) {
            return null;
        }
        $version = InstalledVersions::getPrettyVersion('runwisp/runwisp');

        return $version === null ? null : ltrim($version, 'v');
    }

    /**
     * Runs a runwisp command that prints a JSON document and returns it decoded,
     * also when the command exits non-zero (an invalid config). Throws when the
     * binary can't answer at all.
     *
     * @param list<string> $args
     */
    public static function json(string $binary, array $args, ?string $input = null): mixed
    {
        $process = new Process([$binary, ...$args], null, null, $input);
        try {
            $process->run();
        } catch (\Throwable $e) {
            throw new \RuntimeException("could not run $binary: {$e->getMessage()}", 0, $e);
        }
        if ($process->getExitCode() === 127) {
            throw new \RuntimeException("could not run $binary: " . self::INSTALL_HINT);
        }
        $doc = json_decode($process->getOutput(), true);
        if ($doc === null) {
            // An older runwisp without the command or flag, say.
            throw new \RuntimeException("could not run runwisp {$args[0]} with $binary: " . trim($process->getErrorOutput()));
        }

        return $doc;
    }
}
