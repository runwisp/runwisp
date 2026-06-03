// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { execSync } from "node:child_process";
import {
  existsSync,
  mkdirSync,
  readFileSync,
  statSync,
  writeFileSync,
} from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import openapiTS, { astToString } from "openapi-typescript";

const __dirname = dirname(fileURLToPath(import.meta.url));
const rootDir = join(__dirname, "..");
const appDir = join(rootDir, "../../apps/runwisp");
const openapiSpec = join(appDir, "openapi.json");
const outputFile = join(rootDir, "src/generated/api.ts");

function specIsUsable(path: string): boolean {
  if (!existsSync(path)) return false;
  return statSync(path).size > 0;
}

// Generate the OpenAPI spec from the Go app if needed or when --fresh is set.
// Treat an empty file as missing — a prior failed run can leave a 0-byte file
// behind, which would otherwise blow up at JSON.parse with "Unexpected EOF".
if (!specIsUsable(openapiSpec) || process.argv.includes("--fresh")) {
  console.log("Generating OpenAPI spec from Go app...");
  execSync("./scripts/generate-openapi.sh", {
    cwd: appDir,
    stdio: "inherit",
  });
}

if (!specIsUsable(openapiSpec)) {
  console.error("OpenAPI spec missing or empty at", openapiSpec);
  process.exit(1);
}

const outputDir = dirname(outputFile);
if (!existsSync(outputDir)) {
  mkdirSync(outputDir, { recursive: true });
}

type OpenAPISpec = Extract<
  Parameters<typeof openapiTS>[0],
  Record<string, unknown>
>;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

function isOpenAPISpec(value: unknown): value is OpenAPISpec {
  return (
    isRecord(value) &&
    typeof value.openapi === "string" &&
    isRecord(value.info) &&
    isRecord(value.paths)
  );
}

console.log("Generating TypeScript types from OpenAPI spec...");
const parsedSpec: unknown = JSON.parse(readFileSync(openapiSpec, "utf-8"));
if (!isOpenAPISpec(parsedSpec)) {
  throw new Error(
    `OpenAPI spec at ${openapiSpec} is not a valid OpenAPI document`,
  );
}
const spec = parsedSpec;
const ast = await openapiTS(spec);
writeFileSync(outputFile, astToString(ast));

// Append convenience type aliases
const content = readFileSync(outputFile, "utf-8");
const aliases = `
// Convenience type aliases for common response/request schemas
export type Schemas = components["schemas"];
export type Task = Schemas["TaskResponse"];
export type Run = Schemas["Run"];
export type DaemonInfo = Schemas["DaemonInfo"];
export type SystemStats = Schemas["SystemStats"];
`;
writeFileSync(outputFile, content + aliases);

console.log("Generated:", outputFile);
