<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\Symfony;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;

#[AsCommand('runwisp:check', 'Check the RunWisp config and list the tasks runwisp:work would run')]
final class CheckCommand extends WorkCommand
{
    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        return $this->worker($output)?->check() ?? self::FAILURE;
    }
}
