<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\Laravel;

use Illuminate\Console\Scheduling\Schedule;

/** Checks the config and lists the tasks runwisp:work would run, without running any. */
final class CheckCommand extends WorkCommand
{
    /** @var string */
    protected $signature = 'runwisp:check';

    /** @var string */
    protected $description = 'Check the RunWisp config and list the tasks runwisp:work would run';

    public function handle(Schedule $schedule): int
    {
        return $this->worker($schedule)->check();
    }
}
