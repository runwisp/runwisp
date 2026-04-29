#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)

module=$(cd "${script_dir}/.." && go list -m -f '{{.Path}}')

# Ensure the UI dist folder has at least one file to satisfy go:embed during build
dist_dir="${script_dir}/../internal/ui/dist"
mkdir -p "${dist_dir}"
if [ -z "$(ls -A "${dist_dir}")" ]; then
  echo '<!-- placeholder for go:embed -->' > "${dist_dir}/index.html"
fi

# Use a fixed version so the checked-in openapi.json stays deterministic
# (the real binary version is injected at build time via metadata.sh).
go run -ldflags "-X ${module}/internal/version.Version=dev" ./cmd/runwisp openapi > openapi.json
