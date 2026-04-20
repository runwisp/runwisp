#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)

# shellcheck source=./metadata.sh
source "${script_dir}/metadata.sh"

CGO_ENABLED=0 go build -ldflags "$(runner_ldflags)" -o runwisp ./cmd/runwisp
