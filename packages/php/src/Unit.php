<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp;

/** A task or service this app defines, and how the worker starts one run of it. */
final class Unit
{
    /**
     * @param array<string, mixed> $options [tasks.*] or [services.*] keys, as in runwisp.toml
     * @param list<string>         $argv    the child process that runs it once; it finds the run in RUNWISP_RUN
     */
    public function __construct(
        public readonly string $name,
        public readonly array $options,
        public readonly array $argv,
        public readonly bool $service = false,
    ) {
    }
}
