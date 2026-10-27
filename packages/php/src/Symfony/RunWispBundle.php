<?php

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

declare(strict_types=1);

namespace RunWisp\Symfony;

use RunWisp\Settings;
use Symfony\Component\Config\Definition\Configurator\DefinitionConfigurator;
use Symfony\Component\DependencyInjection\ContainerBuilder;
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;
use Symfony\Component\HttpKernel\Bundle\AbstractBundle;

use function Symfony\Component\DependencyInjection\Loader\Configurator\service;
use function Symfony\Component\DependencyInjection\Loader\Configurator\tagged_locator;

/**
 * Runs the app's Symfony Scheduler schedules under RunWisp: `bin/console
 * runwisp:work`, checked by `runwisp:check`. Options left out fall back to the
 * RUNWISP_* env vars.
 */
final class RunWispBundle extends AbstractBundle
{
    protected string $extensionAlias = 'runwisp';

    public function configure(DefinitionConfigurator $definition): void
    {
        $definition->rootNode()
            ->children()
                ->scalarNode('app')->end()
                ->scalarNode('data')->end()
                ->integerNode('port')->end()
                ->scalarNode('host')->end()
                ->scalarNode('password')->end()
                ->booleanNode('auth')->end()
                ->scalarNode('binary')->end()
                ->variableNode('config')->defaultValue([])->info('The rest of a runwisp.toml: notifiers, defaults, shell tasks')->end()
            ->end();
    }

    /** @param array<string, mixed> $config */
    public function loadExtension(array $config, ContainerConfigurator $container, ContainerBuilder $builder): void
    {
        $settings = array_filter(
            array_intersect_key($config, array_flip(Settings::OPTIONS)),
            static fn (mixed $value): bool => $value !== null,
        );
        $services = $container->services();
        $services->set('runwisp.schedules', Schedules::class)
            ->args([tagged_locator('scheduler.schedule_provider', 'name')]);
        $workArgs = [service('runwisp.schedules'), $settings, $config['config'] ?? [], '%kernel.project_dir%', '%kernel.environment%', '%kernel.debug%'];
        $services->set('runwisp.command.work', WorkCommand::class)->args($workArgs)->tag('console.command');
        $services->set('runwisp.command.check', CheckCommand::class)->args($workArgs)->tag('console.command');
        $services->set('runwisp.command.run', RunCommand::class)
            ->args([service('runwisp.schedules'), service('messenger.default_bus')->nullOnInvalid()])
            ->tag('console.command');
    }
}
