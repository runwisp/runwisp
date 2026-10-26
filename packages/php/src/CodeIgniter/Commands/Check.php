<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\CodeIgniter\Commands;

use CodeIgniter\CLI\BaseCommand;

/** Checks the config and lists the tasks runwisp:work would run, without running any. */
class Check extends BaseCommand
{
    protected $group = 'RunWisp';
    protected $name = 'runwisp:check';
    protected $description = 'Checks the RunWisp config and lists the tasks runwisp:work would run.';
    protected $usage = 'runwisp:check';

    /** @param array<int|string, string|null> $params */
    public function run(array $params): int
    {
        return Work::worker()->check();
    }
}
