#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)

# shellcheck source=./ensure-ui-dist.sh
source "${script_dir}/ensure-ui-dist.sh"
ensure_real_ui_dist

module=$(cd "${script_dir}/.." && go list -m -f '{{.Path}}')

# Use a fixed version so the checked-in openapi.json stays deterministic
# (the real binary version is injected at build time via metadata.sh).
# Write to a temp file and rename on success so a failed run doesn't leave
# behind an empty openapi.json (which poisons subsequent builds).
tmp=$(mktemp openapi.json.XXXXXX)
trap 'rm -f "${tmp}"' EXIT
go run -ldflags "-X ${module}/internal/version.Version=dev" ./cmd/runwisp openapi > "${tmp}"
mv "${tmp}" openapi.json
