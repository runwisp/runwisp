// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// An Astro integration that starts the tasks when the server starts, instead
// of on the first request: in `astro dev`, and in the Node adapter's built server.

export interface RunWispAstroOptions {
    /** The module that registers the tasks, from the project root. */
    tasks: string;
}

/** The part of Vite's dev server the integration uses. */
interface DevServer {
    httpServer: { once(event: "listening", listener: () => void): unknown } | null;
    // A DevEnvironment; only a runnable one has a runner.
    environments: { ssr: object };
}

interface ModuleRunner {
    import(id: string): Promise<unknown>;
}

/** The part of a Vite plugin the integration adds to the server build. */
interface VitePlugin {
    name: string;
    transform(code: string, file: string): string | undefined;
}

interface RunWispAstroIntegration {
    name: string;
    hooks: {
        "astro:config:done": (options: {
            config: { adapter?: { name: string } | undefined };
        }) => void;
        "astro:server:setup": (options: { server: DevServer }) => void;
        "astro:build:start": () => void;
        "astro:build:setup": (options: {
            vite: { plugins?: unknown[] };
            target: "client" | "server";
        }) => void;
    };
}

const NODE_ADAPTER = "@astrojs/node";

function isModuleRunner(runner: unknown): runner is ModuleRunner {
    return (
        typeof runner === "object" &&
        runner !== null &&
        "import" in runner &&
        typeof runner.import === "function"
    );
}

export default function runwisp(options: RunWispAstroOptions): RunWispAstroIntegration {
    // Vite resolves "/src/tasks.ts" from the project root.
    const id = `/${options.tasks.replace(/^\.?\/+/, "")}`;
    let adapter: string | undefined;
    return {
        name: "@runwisp/js",
        hooks: {
            "astro:config:done": ({ config }) => {
                adapter = config.adapter?.name;
            },
            "astro:server:setup": ({ server }) => {
                server.httpServer?.once("listening", () => {
                    const { ssr } = server.environments;
                    const runner = "runner" in ssr ? ssr.runner : undefined;
                    if (!isModuleRunner(runner)) {
                        console.error(
                            `runwisp: can't load ${id}: the dev server has no SSR runner`,
                        );
                        return;
                    }
                    runner.import(id).catch((err: unknown) => {
                        console.error(err);
                    });
                });
            },
            // Only the Node adapter's server starts the tasks: with any other
            // build, they would never run.
            "astro:build:start": () => {
                if (adapter === NODE_ADAPTER) return;
                throw new Error(
                    `@runwisp/js/astro: tasks run only with the ${NODE_ADAPTER} adapter, and this build uses ${adapter ?? "no adapter"}. Use ${NODE_ADAPTER}, or remove the runwisp integration.`,
                );
            },
            "astro:build:setup": ({ vite, target }) => {
                if (target !== "server") return;
                const plugin: VitePlugin = {
                    name: "@runwisp/js",
                    // The Node adapter's server module runs when the built
                    // server starts, standalone and in middleware mode.
                    transform: (code, file) =>
                        file.endsWith(`/${NODE_ADAPTER}/dist/server.js`)
                            ? `import ${JSON.stringify(id)};\n${code}`
                            : undefined,
                };
                (vite.plugins ??= []).push(plugin);
            },
        },
    };
}
