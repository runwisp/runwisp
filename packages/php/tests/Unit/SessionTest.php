<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\Tests\Unit;

use PHPUnit\Framework\TestCase;
use RunWisp\Session;

final class SessionTest extends TestCase
{
    public function testRefusalExplainsAProtocolMismatch(): void
    {
        $error = Session::refusal('426', "HTTP/1.1 426 Upgrade Required\r\nUpgrade: runwisp-app/2", '', ['pid' => 42, 'version' => '2.0.0']);
        self::assertStringContainsString('RunWisp 2.0.0 (pid 42) speaks app protocol 2', $error->getMessage());
        self::assertStringContainsString('kill 42', $error->getMessage());
    }

    public function testRefusalCarriesTheDaemonsReason(): void
    {
        $error = Session::refusal('409', 'HTTP/1.1 409 Conflict', "another app uses this RunWisp daemon\n");
        self::assertSame('RunWisp refused the app connection (HTTP 409): another app uses this RunWisp daemon', $error->getMessage());
    }
}
