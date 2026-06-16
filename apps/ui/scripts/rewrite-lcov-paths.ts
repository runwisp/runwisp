// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0
//
// rewrite-lcov-paths.ts — make shared-package coverage resolvable in SonarCloud.
//
// Our vitest run also instruments the shared packages/* sources our tests import
// (run-helpers.ts, format.ts, …). v8 writes those as `SF:../../packages/...` paths
// relative to apps/ui. SonarCloud is anchored at the repo root and can't resolve
// `../`-escaped paths, so it scored every shared file 0%. Stripping the leading
// `../` segments makes the SF paths repo-root-relative, and Sonar then attributes
// their real coverage. apps/ui's own `src/...` entries have no `../` prefix and are
// left untouched.

import { readFileSync, writeFileSync } from "node:fs";

const lcovPath = "coverage/lcov.info";
const original = readFileSync(lcovPath, "utf8");
const rewritten = original.replace(/^SF:(?:\.\.\/)+/gm, "SF:");
writeFileSync(lcovPath, rewritten);
