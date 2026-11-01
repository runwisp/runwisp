<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\Laravel;

use Symfony\Component\Process\Process;

/** A crontab that still runs Laravel's schedule next to RunWisp runs every task twice. */
final class LeftoverCron
{
    /**
     * The lines of these crontabs that run `schedule:run` or `schedule:work`,
     * comments left out.
     *
     * @return list<string>
     */
    public static function find(string ...$crontabs): array
    {
        $found = [];
        foreach ($crontabs as $crontab) {
            foreach (preg_split('/\R/', $crontab) ?: [] as $line) {
                $line = trim($line);
                if ($line !== '' && $line[0] !== '#' && preg_match('/\bschedule:(run|work)\b/', $line) === 1) {
                    $found[] = $line;
                }
            }
        }

        return $found;
    }

    /** The warning for the host's crontabs, or null when none runs the schedule. */
    public static function warning(): ?string
    {
        $crontabs = [];
        try {
            $process = new Process(['crontab', '-l'], null, null, null, 5);
            $process->run();
            $crontabs[] = $process->isSuccessful() ? $process->getOutput() : '';
        } catch (\Throwable) {
            // No crontab command, or none for this user: nothing to find.
        }
        foreach (['/etc/crontab', ...(glob('/etc/cron.d/*') ?: [])] as $file) {
            $crontabs[] = is_file($file) && is_readable($file) ? (string) @file_get_contents($file) : '';
        }
        $line = self::find(...$crontabs)[0] ?? null;

        return $line === null ? null : "runwisp: a crontab still runs schedule:run ($line); remove it, or every scheduled task runs twice";
    }
}
