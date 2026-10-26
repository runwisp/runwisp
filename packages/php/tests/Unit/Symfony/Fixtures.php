<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\Tests\Unit\Symfony;

use RunWisp\Symfony\RunWispBundle;
use RunWisp\Task;
use Symfony\Bundle\FrameworkBundle\FrameworkBundle;
use Symfony\Bundle\FrameworkBundle\Kernel\MicroKernelTrait;
use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;
use Symfony\Component\HttpKernel\Kernel;
use Symfony\Component\Messenger\Attribute\AsMessageHandler;
use Symfony\Component\Scheduler\Attribute\AsCronTask;
use Symfony\Component\Scheduler\Attribute\AsPeriodicTask;
use Symfony\Component\Scheduler\Attribute\AsSchedule;
use Symfony\Component\Scheduler\RecurringMessage;
use Symfony\Component\Scheduler\Schedule;
use Symfony\Component\Scheduler\ScheduleProviderInterface;
use Symfony\Component\Scheduler\Trigger\CallbackTrigger;

final class TestKernel extends Kernel
{
    use MicroKernelTrait;

    public function registerBundles(): iterable
    {
        return [new FrameworkBundle(), new RunWispBundle()];
    }

    public function getProjectDir(): string
    {
        return __DIR__;
    }

    public function getCacheDir(): string
    {
        return sys_get_temp_dir() . '/runwisp-symfony-test/' . $this->environment;
    }

    public function getLogDir(): string
    {
        return $this->getCacheDir();
    }

    protected function configureContainer(ContainerConfigurator $container): void
    {
        $container->extension('framework', ['test' => true, 'messenger' => ['enabled' => true], 'scheduler' => ['enabled' => true]]);
        $container->extension('runwisp', ['port' => 19999]);
        $container->services()->defaults()->autowire()->autoconfigure()->public()
            ->set(NightlyCommand::class)
            ->set(Reports::class)
            ->set(Cleaner::class)
            ->set(PingHandler::class);
    }
}

#[AsCommand('nightly')]
#[AsCronTask('0 3 * * *', timezone: 'Europe/Bratislava', jitter: 30)]
#[Task(retry_attempts: 2)]
final class NightlyCommand extends Command
{
    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        $output->writeln('nightly ran');

        return self::SUCCESS;
    }
}

final class Cleaner
{
    public static int $calls = 0;

    #[AsPeriodicTask(frequency: 90)]
    #[Task('clean', timeout: '5m')]
    public function clean(): void
    {
        self::$calls++;
    }
}

final class Ping
{
}

#[AsMessageHandler]
final class PingHandler
{
    public static int $calls = 0;

    public function __invoke(Ping $ping): void
    {
        self::$calls++;
    }
}

#[AsSchedule('reports')]
final class Reports implements ScheduleProviderInterface
{
    public function getSchedule(): Schedule
    {
        return (new Schedule())->add(
            RecurringMessage::cron('*/5 * * * *', new Ping()),
            RecurringMessage::every('1 month', new Ping()),
            RecurringMessage::trigger(new CallbackTrigger(static fn () => null), new Ping()),
        );
    }
}
