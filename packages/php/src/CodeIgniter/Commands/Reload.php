<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\CodeIgniter\Commands;

use CodeIgniter\CLI\BaseCommand;

/** Makes the running runwisp:work read the tasks again: what to run after a deploy. */
class Reload extends BaseCommand
{
    protected $group = 'RunWisp';
    protected $name = 'runwisp:reload';
    protected $description = 'Makes the running runwisp:work read the tasks again, after a deploy.';
    protected $usage = 'runwisp:reload';

    /** @param array<int|string, string|null> $params */
    public function run(array $params): int
    {
        return Work::worker()->reload();
    }
}
