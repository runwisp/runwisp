// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { execSync } from "node:child_process";
import {
  mkdirSync,
  readdirSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
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

const goFileHeader = `// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0
//
// Code generated from packages/asyncapi/asyncapi.yaml; DO NOT EDIT.

`;

function fixGoNamingConvention(match, indent, fieldName) {
  if (fieldName[0] < "A" || fieldName[0] > "Z") return match;
  const fixed = fieldName.replace(/Id$/, "ID").replace(/Url$/, "URL");
  return `${indent}${fixed}`;
}

function injectGoImports(content) {
  const needsTime =
    content.includes("*time.Time") && !content.includes('"time"');
  const needsJSON =
    content.includes("json.RawMessage") &&
    !content.includes('"encoding/json"');
  if (!needsTime && !needsJSON) {
    return content;
  }
  const imports = [];
  if (needsJSON) imports.push('\t"encoding/json"');
  if (needsTime) imports.push('\t"time"');
  if (content.includes("import (")) {
    return content.replace(/import \(/, `import (\n${imports.join("\n")}`);
  }
  return content.replace(
    /^(package protocol\n)/m,
    `$1\nimport (\n${imports.join("\n")}\n)\n`,
  );
}

const int64Fields = new Set(["Limit", "LogSize", "FromLine", "N", "Ts"]);
const timeFields = new Set(["StartedAt", "FinishedAt"]);

// isAdditionalPropertiesBoilerplate identifies the generator-injected field that
// must be dropped. Using string checks avoids regex quantifier overhead.
function isAdditionalPropertiesBoilerplate(trimmed) {
  return (
    trimmed.startsWith("AdditionalProperties") &&
    trimmed.includes("map[string]interface{}") &&
    trimmed.includes('json:"-,omitempty"')
  );
}

// applyGoNamingConvention renames Id→ID and Url→URL suffixes on exported
// struct field names. Returns the fixed line, or null if no rename was needed.
function applyGoNamingConvention(fieldName, rest, indent) {
  if (fieldName[0] < "A" || fieldName[0] > "Z") return null;
  if (fieldName.endsWith("Id"))
    return indent + fieldName.slice(0, -2) + "ID" + rest;
  if (fieldName.endsWith("Url"))
    return indent + fieldName.slice(0, -3) + "URL" + rest;
  return null;
}

function transformGoLine(line) {
  const trimmed = line.trimStart();
  if (!trimmed) return line;
  const indent = line.slice(0, line.length - trimmed.length);

  if (isAdditionalPropertiesBoilerplate(trimmed)) return "";

  // First word on the line is the struct field name.
  let nameEnd = 0;
  while (nameEnd < trimmed.length && trimmed[nameEnd] !== " " && trimmed[nameEnd] !== "\t")
    nameEnd++;
  const fieldName = trimmed.slice(0, nameEnd);
  const rest = trimmed.slice(nameEnd);

  const renamed = applyGoNamingConvention(fieldName, rest, indent);
  if (renamed !== null) return renamed;

  if (timeFields.has(fieldName) && rest.includes(" string "))
    return indent + fieldName + rest.replace(" string ", " *time.Time ");

  if (fieldName === "ExitCode" && rest.includes(" int "))
    return indent + fieldName + rest.replace(" int ", " *int ");

  if (fieldName === "Script" && rest.includes("map[string]interface{}"))
    return indent + 'Script json.RawMessage\t`json:"script" binding:"required"`';

  if (int64Fields.has(fieldName) && rest.includes(" int "))
    return indent + fieldName + rest.replace(" int ", " int64 ");

  return line;
}

function postProcessGoFile(filePath) {
  let content = readFileSync(filePath, "utf-8");
  if (!content.startsWith("// SPDX-FileCopyrightText")) {
    content = goFileHeader + content;
  }
  content = content.replaceAll("ReservedType", "Type");
  content = content.split("\n").map(transformGoLine).join("\n");
  content = injectGoImports(content);
  writeFileSync(filePath, content);
}

function postProcessGo(dir) {
  const files = readdirSync(dir).filter((f) => f.endsWith(".go"));
  for (const file of files) {
    postProcessGoFile(join(dir, file));
  }
  execSync(`gofmt -w .`, { cwd: dir });
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

function objectToZod(schema) {
  if (schema.properties) {
    const props = Object.entries(schema.properties).map(([key, value]) => {
      let zType = typeToZod(value, key.endsWith("At") && key !== "sentAt");
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

  outCode = outCode.replaceAll(
    "v: z.number().int().optional()",
    "v: z.literal(PROTOCOL_VERSION).optional()",
  );
  outCode = outCode.replaceAll(
    '"v": z.number().int().optional()',
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

await run();
