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
    if (schema.allOf) return objectToZod(schema);
    if (schema.const) return `z.literal(${JSON.stringify(schema.const)})`;
    if (schema.enum)
        return `z.enum([${schema.enum.map((value) => JSON.stringify(value)).join(", ")}])`;
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
    return "z.unknown()";
}

async function generateZod(resolved) {
    const inboundMessages =
        resolved.channels.daemonToStation.subscribe.message.oneOf.map(
            (msg) => msg.payload,
        );
    const outboundMessages =
        resolved.channels.stationToDaemon.publish.message.oneOf.map(
            (msg) => msg.payload,
        );

    const inboundZodObjs = inboundMessages.map((msg) => typeToZod(msg));
    const outboundZodObjs = outboundMessages.map((msg) => typeToZod(msg));

    let outCode = `// Generated from asyncapi.yaml
import { z } from "zod";

export const PROTOCOL_VERSION = 2;

export const inboundDaemonMessageSchema = z.discriminatedUnion("type", [
  ${inboundZodObjs.join(",\n  ")}
]);

export type InboundDaemonMessage = z.infer<typeof inboundDaemonMessageSchema>;

export const outboundDaemonMessageSchema = z.discriminatedUnion("type", [
  ${outboundZodObjs.join(",\n  ")}
]);

export type OutboundDaemonMessage = z.infer<typeof outboundDaemonMessageSchema>;
`;

    outCode = outCode.replaceAll(
        "protocolVersion: z.number().int().optional()",
        "protocolVersion: z.literal(PROTOCOL_VERSION).optional()",
    );
    outCode = outCode.replaceAll(
        '"protocolVersion": z.number().int().optional()',
        '"protocolVersion": z.literal(PROTOCOL_VERSION).optional()',
    );

    writeFileSync(tsOutputFile, outCode);
}

function writeGo(document) {
    resetDir(goOutputDir);
    const source = execSync("gofmt", { input: generateGo(document) });
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
