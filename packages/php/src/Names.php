<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp;

/** Task names for entries a framework's scheduler defines without one RunWisp accepts. */
final class Names
{
    /** @var array<string, true> */
    private array $taken = [];

    /** @param (\Closure(string): void)|null $warn told about a name that had to change */
    public function __construct(private readonly ?\Closure $warn = null)
    {
    }

    /** $raw as a task name: 1 to 100 of a-z A-Z 0-9 . _ : - */
    public static function slug(string $raw): string
    {
        $name = trim((string) preg_replace('/[^A-Za-z0-9._:-]+/', '-', $raw), '-');

        return $name === '' ? 'task' : substr($name, 0, 100);
    }

    /**
     * A free name from $raw: its slug, or with -2, -3 when another entry took
     * it. A renamed entry keeps its runs under the new name only, so it warns.
     */
    public function claim(string $raw): string
    {
        $name = self::slug($raw);
        $base = $name;
        for ($n = 2; isset($this->taken[$name]); $n++) {
            $name = substr($base, 0, 100 - strlen("-$n")) . "-$n";
        }
        $this->taken[$name] = true;
        if ($name !== $base && $this->warn !== null) {
            ($this->warn)("runwisp: two scheduled entries are named \"$base\"; the second is \"$name\". Name them to keep their run history apart.");
        }

        return $name;
    }
}
