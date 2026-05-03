// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { execSync } from "child_process";
import {
  mkdirSync,
  readdirSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "fs";
import { join, dirname } from "path";
import { fileURLToPath } from "url";
import yaml from "yaml";
import $RefParser from "@apidevtools/json-schema-ref-parser";
import { GO_COMMON_PRESET, GoFileGenerator } from "@asyncapi/modelina";

const __dirname = dirname(fileURLToPath(import.meta.url));
const rootDir = join(__dirname, "..");
const asyncApiFile = join(rootDir, "asyncapi.yaml");

const goOutputDir = join(
  rootDir,
  "../../apps/runwisp/internal/generated/protocol",
);
const goPackageName = "protocol";
const tsOutputFile = join(rootDir, "src/generated/protocol-messages.ts");
const tsOutputDir = dirname(tsOutputFile);

const goGenerator = new GoFileGenerator({
  presets: [{ preset: GO_COMMON_PRESET, options: { addJsonTag: true } }],
});

function loadAsyncApiDocument() {
  return yaml.parse(readFileSync(asyncApiFile, "utf-8"));
}

function resolveAsyncApiDocument(document) {
  return $RefParser.dereference(structuredClone(document));
}

function getMessageSchemaNames(document) {
  const schemaRefPrefix = "#/components/schemas/";

  return [
    ...new Set(
      Object.values(document.components.messages).map((message) => {
        if (!message.payload?.$ref?.startsWith(schemaRefPrefix)) {
          throw new Error(
            "Expected message payload to reference components.schemas",
          );
        }

        return message.payload.$ref.slice(schemaRefPrefix.length);
      }),
    ),
  ];
}

function ensureDir(dir) {
  mkdirSync(dir, { recursive: true });
}

function resetDir(dir) {
  rmSync(dir, { recursive: true, force: true });
  ensureDir(dir);
}

function postProcessGo(dir) {
  const files = readdirSync(dir).filter((f) => f.endsWith(".go"));
  for (const file of files) {
    const filePath = join(dir, file);
    let content = readFileSync(filePath, "utf-8");

    // Fix reserved keyword workaround from Modelina
    content = content.replace(/ReservedType/g, "Type");

    // Remove AdditionalProperties fields (json:"-" makes them useless noise)
    content = content.replace(
      /\s*AdditionalProperties\s+map\[string\]interface\{\}\s+`json:"-,omitempty"`\n/g,
      "\n",
    );

    // Fix Go naming conventions: trailing Id -> ID, Url -> URL
    content = content.replace(
      /^(\s+)(\w*(?:Id|Url))\b/gm,
      (match, indent, fieldName) => {
        if (fieldName[0] < "A" || fieldName[0] > "Z") return match;
        const fixed = fieldName
          .replace(/Id$/, "ID")
          .replace(/Url$/, "URL");
        return `${indent}${fixed}`;
      },
    );

    // Nullable date-time fields -> *time.Time
    // The spec marks startedAt, finishedAt as nullable + format: date-time
    content = content.replace(
      /(\s+(?:StartedAt|FinishedAt))\s+string(\s+`json:"(?:startedAt|finishedAt),omitempty"`)/g,
      "$1 *time.Time$2",
    );

    // Nullable integer fields -> pointer types
    // The spec marks exitCode as nullable
    content = content.replace(
      /(\s+ExitCode)\s+int(\s+`json:"exitCode,omitempty"`)/g,
      "$1 *int$2",
    );

    // Script field: map[string]interface{} -> json.RawMessage for passthrough
    content = content.replace(
      /(\s+Script)\s+map\[string\]interface\{\}(\s+`json:"script)[,"].*`/g,
      '$1 json.RawMessage$2" binding:"required"`',
    );

    // int -> int64 for fields with format: int64 (offset, limit, logSize)
    content = content.replace(
      /(\s+(?:Offset|Limit|LogSize))\s+int(\s+`json:"(?:offset|limit|logSize))/g,
      "$1 int64$2",
    );

    // Add required imports based on content
    const needsTime =
      content.includes("*time.Time") && !content.includes('"time"');
    const needsJSON =
      content.includes("json.RawMessage") &&
      !content.includes('"encoding/json"');
    if (needsTime || needsJSON) {
      const imports = [];
      if (needsJSON) imports.push('\t"encoding/json"');
      if (needsTime) imports.push('\t"time"');

      if (content.includes("import (")) {
        content = content.replace(
          /import \(/,
          `import (\n${imports.join("\n")}`,
        );
      } else {
        content = content.replace(
          /^(package protocol\n)/m,
          `$1\nimport (\n${imports.join("\n")}\n)\n`,
        );
      }
    }

    writeFileSync(filePath, content);
  }
  execSync(`gofmt -w .`, { cwd: dir });
}

function typeToZod(schema, isDate = false) {
  if (!schema) return "z.unknown()";
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
  if (schema.type === "boolean") return "z.boolean()";
  if (schema.type === "object") {
    if (schema.properties) {
      const props = Object.entries(schema.properties).map(([key, value]) => {
        let zType = typeToZod(value, key.endsWith("At") && key !== "sentAt");
        if (value.nullable) zType += ".nullable()";
        if (!schema.required || !schema.required.includes(key)) {
          zType += ".optional()";
        }
        return `"${key}": ${zType}`;
      });
      return `z.object({ ${props.join(", ")} })`;
    }
    if (schema.additionalProperties) {
      return `z.record(z.string(), ${typeToZod(schema.additionalProperties)})`;
    }
    return "z.unknown()";
  }
  return "z.unknown()";
}

async function generateZod(resolved) {
  const inboundMessages =
    resolved.channels.daemonToCloud.subscribe.message.oneOf.map(
      (msg) => msg.payload,
    );
  const outboundMessages =
    resolved.channels.cloudToDaemon.publish.message.oneOf.map(
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

  outCode = outCode.replace(
    /v: z\.number\(\)\.int\(\)\.optional\(\)/g,
    "v: z.literal(PROTOCOL_VERSION).optional()",
  );
  outCode = outCode.replace(
    /"v": z\.number\(\)\.int\(\)\.optional\(\)/g,
    '"v": z.literal(PROTOCOL_VERSION).optional()',
  );

  writeFileSync(tsOutputFile, outCode);
}

async function generateGo(resolved, rootSchemaNames) {
  resetDir(goOutputDir);

  for (const schemaName of rootSchemaNames) {
    const schema = resolved.components.schemas[schemaName];
    await goGenerator.generateToFiles(
      { ...schema, $id: schema.$id ?? schemaName },
      goOutputDir,
      { packageName: goPackageName },
    );
  }

  postProcessGo(goOutputDir);
}

async function run() {
  const document = loadAsyncApiDocument();
  const resolvedDocument = await resolveAsyncApiDocument(document);
  const goRootSchemaNames = getMessageSchemaNames(document);

  console.log("Generating TypeScript types with Zod schemas...");
  ensureDir(tsOutputDir);
  await generateZod(resolvedDocument);

  console.log("Generating Go types...");
  await generateGo(resolvedDocument, goRootSchemaNames);

  console.log("Done!");
}

run().catch(console.error);
