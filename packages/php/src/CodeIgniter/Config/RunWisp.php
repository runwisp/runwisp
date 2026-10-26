<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\CodeIgniter\Config;

use CodeIgniter\Config\BaseConfig;
use RunWisp\Settings;

/**
 * RunWisp's settings. Extend it in app/Config/RunWisp.php to change them.
 * A null setting falls back to its RUNWISP_* env var, then to the default.
 *
 * @phpstan-import-type Options from Settings
 */
class RunWisp extends BaseConfig
{
    /** Which app this is: copies of one app share a RunWisp. Default: the composer.json name. */
    public ?string $app = null;

    /** Data directory: database, run logs and socket. Default: WRITEPATH/runwisp. */
    public ?string $data = null;

    /** Web UI port. Default: 9477, or the next free one. */
    public ?int $port = null;

    /** Web UI bind address. Default: 127.0.0.1. */
    public ?string $host = null;

    /** Web UI password. Default: a new one on each start. */
    public ?string $password = null;

    /** false turns the login off, like RUNWISP_AUTH=off. */
    public ?bool $auth = null;

    /** Path to the runwisp binary. */
    public ?string $binary = null;

    /**
     * The rest of a runwisp.toml, with the same keys: notifiers, route,
     * defaults and shell tasks. A scheduled task's own keys, such as
     * retry_attempts, go in #[RunWisp\Task] on its command or closure.
     *
     * @var array<string, mixed>
     */
    public array $config = [];

    public function settings(): Settings
    {
        /** @var Options $options */
        $options = array_filter(array_intersect_key(get_object_vars($this), array_flip(Settings::OPTIONS)), static fn (mixed $value): bool => $value !== null);

        // spark runs in public/: the app root is where runwisp.toml would be.
        return Settings::resolve($options, WRITEPATH . 'runwisp', rtrim(ROOTPATH, '/'));
    }
}
