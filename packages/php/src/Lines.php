<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp;

/** Cuts one output stream of a run into the lines the daemon logs. */
final class Lines
{
    /** A line longer than this goes in pieces, well under the daemon's 16 MB message cap. */
    public const MAX_PIECE = 65536;

    private string $rest = '';

    /**
     * The whole lines in what was read so far, and pieces of an overlong one.
     *
     * @return list<array{string, bool}> line, partial (no newline after it)
     */
    public function push(string $text): array
    {
        $out = [];
        $text = $this->rest . $text;
        while (($end = strpos($text, "\n")) !== false) {
            $line = substr($text, 0, $end);
            $text = substr($text, $end + 1);
            self::cut($line, $out);
            $out[] = [$line, false];
        }
        self::cut($text, $out);
        $this->rest = $text;

        return $out;
    }

    /**
     * What is left once the stream ended: a last line without a newline.
     *
     * @return list<array{string, bool}>
     */
    public function flush(): array
    {
        $rest = $this->rest;
        $this->rest = '';

        return $rest === '' ? [] : [[$rest, true]];
    }

    /**
     * Moves whole pieces off the front of an overlong line.
     *
     * @param list<array{string, bool}> $out
     */
    private static function cut(string &$line, array &$out): void
    {
        while (strlen($line) > self::MAX_PIECE) {
            $out[] = [substr($line, 0, self::MAX_PIECE), true];
            $line = substr($line, self::MAX_PIECE);
        }
    }
}
