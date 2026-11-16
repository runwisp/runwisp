// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { execSync } from "node:child_process";
import { mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import yaml from "yaml";
import $RefParser from "@apidevtools/json-schema-ref-parser";
import { generateGo } from "./go.js";

const __dirname = dirname(fileURLToPath(import.meta.url));
const rootDir = join(__dirname, "..");
const asyncApiFile = join(rootDir, "asyncapi.yaml");

const goOutputDir = join(
    rootDir,
    "../../apps/runwisp/internal/generated/protocol",
);
const tsOutputFile = join(rootDir, "src/generated/protocol-messages.ts");
const tsOutputDir = dirname(tsOutputFile);

function loadAsyncApiDocument() {
    return yaml.parse(readFileSync(asyncApiFile, "utf-8"));
}

function resolveAsyncApiDocument(document) {
    return $RefParser.dereference(structuredClone(document));
}

function ensureDir(dir) {
    mkdirSync(dir, { recursive: true });
}

function resetDir(dir) {
    rmSync(dir, { recursive: true, force: true });
    ensureDir(dir);
}

function numberToZod(schema) {
    let base = schema.type === "integer" ? "z.number().int()" : "z.number()";
    if (schema.minimum === 0) {
        base += ".nonnegative()";
    } else if (typeof schema.minimum === "number") {
        base += `.min(${schema.minimum})`;
    }
    if (typeof schema.maximum === "number") {
        base += `.max(${schema.maximum})`;
    }
    return base;
}

// mergeAllOf flattens a dereferenced `allOf` (each entry already inlined by
// $RefParser) into one object schema, so typeToZod can treat schema
// composition the same as a plain object.
function mergeAllOf(schema) {
    const properties = {};
    const required = [];
    for (const sub of schema.allOf) {
        Object.assign(properties, sub.properties ?? {});
        required.push(...(sub.required ?? []));
    }
    return { type: "object", properties, required };
}

function objectToZod(schema) {
    if (schema.allOf) {
        return objectToZod(mergeAllOf(schema));
    }
    if (schema.properties) {
        const props = Object.entries(schema.properties).map(([key, value]) => {
            let zType = typeToZod(
                value,
                key.endsWith("At") && key !== "sentAt",
            );
            if (value.nullable) zType += ".nullable()";
            if (!schema.required?.includes(key)) zType += ".optional()";
            return `"${key}": ${zType}`;
        });
        return `z.object({ ${props.join(", ")} })`;
    }
    if (schema.additionalProperties) {
        return `z.record(z.string(), ${typeToZod(schema.additionalProperties)})`;
    }
    return "z.unknown()";
}

function typeToZod(schema, isDate = false) {
    if (!schema) return "z.unknown()";
    if (schema.oneOf) {
        return `z.union([${schema.oneOf.map((sub) => typeToZod(sub)).join(", ")}])`;
    }
    if (schema.allOf) return objectToZod(schema);
    if (schema.const) return `z.literal(${JSON.stringify(schema.const)})`;
    // Only CLOSED enums are validated: an open one must accept values a
    // newer peer adds (compatibility rule 4).
    if (schema.enum && schema.description?.startsWith("CLOSED"))
        return `z.enum([${schema.enum.map((value) => JSON.stringify(value)).join(", ")}])`;
    if (schema.enum) return "z.string()";
    if (schema.type === "string") {
        if (schema.format === "date-time") {
            return isDate ? "z.coerce.date()" : "z.string()";
        }
        return "z.string()";
    }
    if (schema.type === "integer" || schema.type === "number") {
        return numberToZod(schema);
    }
    if (schema.type === "boolean") return "z.boolean()";
    if (schema.type === "object") return objectToZod(schema);
    if (schema.type === "array") return `z.array(${typeToZod(schema.items)})`;
    return "z.unknown()";
}

async function generateZod(resolved) {
    const entries = (messages) =>
        messages
            .map((msg) => `  ${JSON.stringify(msg.name)}: ${typeToZod(msg.payload)}`)
            .join(",\n");
    const daemonToStation =
        resolved.channels.daemonToStation.subscribe.message.oneOf;
    const stationToDaemon =
        resolved.channels.stationToDaemon.publish.message.oneOf;

    const outCode = `// Generated from asyncapi.yaml
import { z } from "zod";

/** WebSocket subprotocol a v1 daemon offers and a v1 station selects. */
export const SUBPROTOCOL = "runwisp.v1";

/** Every frame; the message payload is \`data\`, validated per type below. */
export const envelopeSchema = ${typeToZod(resolved.components.schemas.Envelope)};

export type Envelope = z.infer<typeof envelopeSchema>;

/** Payload (\`data\`) schemas of the frames a daemon sends, by type. */
export const daemonToStationSchemas = {
${entries(daemonToStation)}
};

/** Payload (\`data\`) schemas of the frames a station sends, by type. */
export const stationToDaemonSchemas = {
${entries(stationToDaemon)}
};
`;

    writeFileSync(tsOutputFile, outCode);
}

function writeGo(document) {
    resetDir(goOutputDir);
    const source = execSync("gofmt", { input: generateGo(document) }); // NOSONAR: dev-time codegen, PATH is the developer's own
    writeFileSync(join(goOutputDir, "protocol.go"), source);
}

async function run() {
    const document = loadAsyncApiDocument();
    const resolvedDocument = await resolveAsyncApiDocument(document);

    console.log("Generating TypeScript types with Zod schemas...");
    ensureDir(tsOutputDir);
    await generateZod(resolvedDocument);

    console.log("Generating Go types...");
    writeGo(document);

    console.log("Done!");
}

await run();
