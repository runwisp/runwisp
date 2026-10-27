<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\Installer;

use Composer\Composer;
use Composer\EventDispatcher\EventSubscriberInterface;
use Composer\IO\IOInterface;
use Composer\Plugin\PluginInterface;
use Composer\Script\ScriptEvents;
use RunWisp\Binary;

/**
 * Downloads the runwisp binary of this package's version for this platform
 * from the GitHub release, verifies it against the release's SHA-256 list,
 * and puts it in the package's bin/, where Binary::resolve() finds it.
 */
final class Plugin implements PluginInterface, EventSubscriberInterface
{
    public const PACKAGE = 'runwisp/runwisp';
    private const RELEASES = 'https://github.com/runwisp/runwisp/releases/download';

    private Composer $composer;
    private IOInterface $io;

    public function activate(Composer $composer, IOInterface $io): void
    {
        $this->composer = $composer;
        $this->io = $io;
    }

    public function deactivate(Composer $composer, IOInterface $io): void
    {
    }

    public function uninstall(Composer $composer, IOInterface $io): void
    {
    }

    public static function getSubscribedEvents(): array
    {
        return [
            ScriptEvents::POST_INSTALL_CMD => 'install',
            ScriptEvents::POST_UPDATE_CMD => 'install',
        ];
    }

    public function install(): void
    {
        $package = $this->composer->getRepositoryManager()->getLocalRepository()->findPackage(self::PACKAGE, '*');
        if ($package === null) {
            return;
        }
        $version = $package->getPrettyVersion();
        // A path repository or a branch: there is no release to match, so runwisp comes from PATH.
        if ($package->isDev()) {
            return;
        }
        $platform = self::platform(PHP_OS_FAMILY, php_uname('m'));
        if ($platform === null) {
            $this->io->writeError('<warning>runwisp/runwisp: no runwisp binary for this platform; ' . Binary::INSTALL_HINT . '</warning>');

            return;
        }
        $bin = $this->composer->getInstallationManager()->getInstallPath($package) . '/bin';
        $target = $bin . '/runwisp-bin';
        $tag = 'v' . ltrim($version, 'v');
        if (is_file($target) && @file_get_contents("$target.version") === $tag) {
            return;
        }
        $this->io->write("<info>runwisp/runwisp: downloading runwisp $tag ($platform)</info>");
        try {
            $http = $this->composer->getLoop()->getHttpDownloader();
            $archive = "runwisp-$platform.tar.gz";
            $checksums = (string) $http->get(self::RELEASES . "/$tag/checksums-sha256.txt")->getBody();
            $tarball = (string) $http->get(self::RELEASES . "/$tag/$archive")->getBody();
            self::place($tarball, self::checksum($checksums, $archive), $target);
            file_put_contents("$target.version", $tag);
            // Tells `runwisp upgrade` to point at Composer instead of replacing the file.
            file_put_contents("$bin/.runwisp-source", "composer\n");
        } catch (\Throwable $e) {
            $this->io->writeError("<warning>runwisp/runwisp: could not install runwisp $tag: {$e->getMessage()}; " . Binary::INSTALL_HINT . '</warning>');
        }
    }

    /** The release's name for this platform, or null when there is no binary for it. */
    public static function platform(string $os, string $machine): ?string
    {
        $os = match ($os) {
            'Linux' => 'linux',
            'Darwin' => 'darwin',
            default => null,
        };
        $arch = match (strtolower($machine)) {
            'x86_64', 'amd64' => 'x64',
            'aarch64', 'arm64' => 'arm64',
            default => null,
        };

        return $os === null || $arch === null ? null : "$os-$arch";
    }

    /** The SHA-256 the release lists for $file. */
    public static function checksum(string $checksums, string $file): string
    {
        foreach (explode("\n", $checksums) as $line) {
            if (preg_match('/^([0-9a-f]{64})\s+\*?(\S+)$/', trim($line), $m) === 1 && $m[2] === $file) {
                return $m[1];
            }
        }

        throw new \RuntimeException("the release lists no checksum for $file");
    }

    /** Verifies the tarball and puts its runwisp binary at $target, replacing it in one step. */
    public static function place(string $tarball, string $sha256, string $target): void
    {
        if (!hash_equals($sha256, hash('sha256', $tarball))) {
            throw new \RuntimeException('the download does not match the release checksum');
        }
        // Next to the target, so the rename is one step on one filesystem.
        $dir = dirname($target) . '/.download-' . bin2hex(random_bytes(8));
        mkdir($dir, 0o700, true);
        try {
            // PharData reads the format from the extension.
            file_put_contents("$dir/runwisp.tar.gz", $tarball);
            (new \PharData("$dir/runwisp.tar.gz"))->extractTo($dir, 'runwisp', true);
            chmod("$dir/runwisp", 0o755);
            if (!rename("$dir/runwisp", $target)) {
                throw new \RuntimeException("could not write $target");
            }
        } finally {
            @unlink("$dir/runwisp.tar.gz");
            @unlink("$dir/runwisp");
            @rmdir($dir);
        }
    }
}
