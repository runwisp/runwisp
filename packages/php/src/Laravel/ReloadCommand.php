<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\Laravel;

use Illuminate\Console\Scheduling\Schedule;

/** Makes the running `runwisp:work` read the schedule again: what to run after a deploy, like `queue:restart`. */
final class ReloadCommand extends WorkCommand
{
    /** @var string */
    protected $signature = 'runwisp:reload';

    /** @var string */
    protected $description = 'Make the running runwisp:work read the schedule again, after a deploy';

    public function handle(Schedule $schedule): int
    {
        return $this->worker($schedule)->reload();
    }
}
