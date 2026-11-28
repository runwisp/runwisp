<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\Tests\Unit;

use PHPUnit\Framework\TestCase;
use RunWisp\Lines;

final class LinesTest extends TestCase
{
    public function testHoldsALineUntilItsNewline(): void
    {
        $lines = new Lines();
        self::assertSame([], $lines->push('hel'));
        self::assertSame([['hello', false], ['world', false]], $lines->push("lo\nworld\nand"));
        self::assertSame([['and', true]], $lines->flush());
        self::assertSame([], $lines->flush());
    }

    public function testCutsAnOverlongLineIntoPartialPieces(): void
    {
        $lines = new Lines();
        $long = str_repeat('a', Lines::MAX_PIECE + 10);
        self::assertSame([[str_repeat('a', Lines::MAX_PIECE), true], ['aaaaaaaaaa', false]], $lines->push("$long\n"));
        self::assertSame([[str_repeat('b', Lines::MAX_PIECE), true]], $lines->push(str_repeat('b', Lines::MAX_PIECE + 1)));
        self::assertSame([['b', true]], $lines->flush());
    }

    public function testKeepsUtf8CharactersIntactAcrossPieceAndReadBoundaries(): void
    {
        $lines = new Lines();
        $prefix = str_repeat('a', Lines::MAX_PIECE - 1);
        self::assertSame([], $lines->push($prefix . "\xe2"));
        self::assertSame([[$prefix, true], ['€', false]], $lines->push("\x82\xac\n"));
    }
}
