#!/usr/bin/env bash
# SPDX-FileCopyrightText: PoppyCake, s.r.o.
# SPDX-License-Identifier: Apache-2.0
# Remove all build outputs, generated code, and caches.
set -euo pipefail

cd "$(dirname -- "${BASH_SOURCE[0]}")/.."

rm -rf \
  .moon/cache \
  .cache \
  .bin \
  apps/runwisp/runwisp \
  apps/runwisp/openapi.json \
  apps/runwisp/dist \
  apps/runwisp/internal/ui/dist \
  apps/runwisp/internal/generated/protocol \
  apps/runwisp/.gocoverdir \
  apps/ui/build \
  apps/ui/.svelte-kit \
  apps/docs/dist \
  apps/docs/.astro \
  apps/docs/public/openapi.json \
  packages/asyncapi/src/generated \
  packages/common/src/generated
