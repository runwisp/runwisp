<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\Laravel;

use Illuminate\Support\ServiceProvider;

/** Adds `runwisp:work`, which runs Laravel's schedule under RunWisp, and `runwisp:check`. */
final class RunWispServiceProvider extends ServiceProvider
{
    private const CONFIG = __DIR__ . '/../../config/runwisp.php';

    public function register(): void
    {
        $this->mergeConfigFrom(self::CONFIG, 'runwisp');
    }

    public function boot(): void
    {
        if (!$this->app->runningInConsole()) {
            return;
        }
        $this->publishes([self::CONFIG => $this->app->configPath('runwisp.php')], 'runwisp-config');
        $this->commands([WorkCommand::class, CheckCommand::class, RunCommand::class]);
    }
}
