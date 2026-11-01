<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\Tests\Unit\Laravel;

use PHPUnit\Framework\TestCase;
use RunWisp\Laravel\LeftoverCron;

final class LeftoverCronTest extends TestCase
{
    public function testFindsLinesThatRunTheSchedule(): void
    {
        $user = "MAILTO=\"\"\n* * * * * cd /var/www/app && php artisan schedule:run >> /dev/null 2>&1\n0 3 * * * /usr/bin/backup\n";
        $system = "# m h dom mon dow user command\n*/5 * * * * www-data /usr/bin/php /var/www/app/artisan schedule:work\n";

        self::assertSame([
            '* * * * * cd /var/www/app && php artisan schedule:run >> /dev/null 2>&1',
            '*/5 * * * * www-data /usr/bin/php /var/www/app/artisan schedule:work',
        ], LeftoverCron::find($user, $system));
    }

    public function testIgnoresCommentsAndOtherCommands(): void
    {
        self::assertSame([], LeftoverCron::find(
            "# * * * * * php artisan schedule:run\n  #  php artisan schedule:work\n* * * * * php artisan schedule:list\n* * * * * php artisan queue:work\n",
            '',
        ));
        self::assertSame([], LeftoverCron::find());
    }
}
