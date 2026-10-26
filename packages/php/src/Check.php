<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp;

/** What `check` prints for a config RunWisp accepts: each unit with its schedule, next run and options. */
final class Check
{
    /**
     * @param array{tasks: array<string, array<string, mixed>>, services: array<string, array<string, mixed>>} $doc
     * @param array<string, array<string, mixed>>                                                         $schedules each task's `runwisp validate --explain` entry, by name
     * @param \Closure(string, string): void                                                               $log
     */
    public static function print(array $doc, array $schedules, \Closure $log): void
    {
        foreach (['tasks' => '', 'services' => ' (service)'] as $kind => $suffix) {
            foreach ($doc[$kind] as $name => $options) {
                $log('info', $name . $suffix);
                if ($kind === 'tasks') {
                    $log('info', '  ' . self::when($options, $schedules[$name] ?? null));
                }
                $shown = array_diff_key($options, ['sdk' => 0, 'cron' => 0, 'timezone' => 0]);
                if ($shown !== []) {
                    $pairs = array_map(static fn (string $key, mixed $value): string => "$key = " . json_encode($value, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE), array_keys($shown), $shown);
                    $log('info', '  ' . implode(', ', $pairs));
                }
            }
        }
        $count = static fn (int $n, string $what): string => $n === 1 ? "1 $what" : "$n {$what}s";
        $log('info', 'runwisp: RunWisp accepts the config: ' . $count(count($doc['tasks']), 'task') . ', ' . $count(count($doc['services']), 'service'));
    }

    /**
     * A task's schedule in words, with its next run in the schedule's zone.
     *
     * @param array<string, mixed>      $options
     * @param array<string, mixed>|null $schedule its `runwisp validate --explain` entry
     */
    public static function when(array $options, ?array $schedule): string
    {
        $cron = $options['cron'] ?? null;
        if (!is_string($cron)) {
            return 'Runs only when started from the web UI, the TUI or the CLI';
        }
        $zone = is_string($schedule['timezone'] ?? null) ? $schedule['timezone'] : '';
        $text = "$cron: " . ($schedule['description'] ?? '') . " ($zone)";
        $next = $schedule['next'][0]['at'] ?? null;
        if (is_string($next)) {
            $at = new \DateTimeImmutable($next);
            // RunWisp may name a zone PHP doesn't know, such as Local: then the time stays as RunWisp gave it.
            if (in_array($zone, \DateTimeZone::listIdentifiers(), true)) {
                $at = $at->setTimezone(new \DateTimeZone($zone));
            }
            $seconds = count((array) preg_split('/\s+/', trim($cron))) > 5;
            $text .= ', next ' . $at->format($seconds ? 'D Y-m-d H:i:s T' : 'D Y-m-d H:i T');
        } elseif (($schedule['fromStart'] ?? false) === true) {
            $text .= ', counted from when RunWisp starts';
        }

        return $text;
    }
}
