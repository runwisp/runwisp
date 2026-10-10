// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { EventEmitter } from "node:events";

import type { AstroIntegration } from "astro";
import { describe, expect, it, vi } from "vitest";

import runwisp from "./astro.ts";

describe("@runwisp/node/astro", () => {
    it("is an Astro integration", () => {
        const integration: AstroIntegration = runwisp({ tasks: "src/tasks.ts" });
        expect(integration.name).toBe("@runwisp/node");
    });

    it("loads the tasks module once the dev server listens", () => {
        const httpServer = new EventEmitter();
        const load = vi.fn((_id: string) => Promise.resolve());
        runwisp({ tasks: "./src/tasks.ts" }).hooks["astro:server:setup"]({
            server: { httpServer, environments: { ssr: { runner: { import: load } } } },
        });
        expect(load).not.toHaveBeenCalled();
        httpServer.emit("listening");
        expect(load).toHaveBeenCalledWith("/src/tasks.ts");
    });

    it("fails the build without the Node adapter, where the tasks would never start", () => {
        const build = (adapter: { name: string } | undefined) => {
            const { hooks } = runwisp({ tasks: "src/tasks.ts" });
            hooks["astro:config:done"]({ config: { adapter } });
            return () => {
                hooks["astro:build:start"]();
            };
        };
        expect(build(undefined)).toThrow("this build uses no adapter");
        expect(build({ name: "@astrojs/vercel" })).toThrow("this build uses @astrojs/vercel");
        expect(build({ name: "@astrojs/node" })).not.toThrow();
    });

    it("imports the tasks module from the Node adapter's server in the server build", () => {
        const hook = runwisp({ tasks: "src/tasks.ts" }).hooks["astro:build:setup"];
        const client: { plugins?: unknown[] } = {};
        hook({ vite: client, target: "client" });
        expect(client.plugins).toBeUndefined();

        const server: { plugins?: unknown[] } = {};
        hook({ vite: server, target: "server" });
        const [plugin] = server.plugins ?? [];
        if (!isPlugin(plugin)) throw new Error("no plugin");
        expect(plugin.transform("start();", "/app/node_modules/@astrojs/node/dist/server.js")).toBe(
            'import "/src/tasks.ts";\nstart();',
        );
        expect(plugin.transform("x", "/app/src/pages/index.astro")).toBeUndefined();
    });
});

function isPlugin(p: unknown): p is { transform(code: string, file: string): string | undefined } {
    return typeof p === "object" && p !== null && "transform" in p;
}
