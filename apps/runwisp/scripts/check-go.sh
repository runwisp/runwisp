#!/usr/bin/env bash
set -euo pipefail

# Ensure the embedded UI dist directory exists so go vet
# can resolve the //go:embed directive without a full UI build.
if [ ! -d internal/ui/dist ]; then
  mkdir -p internal/ui/dist
  echo '<!-- placeholder for go:embed -->' > internal/ui/dist/index.html
fi

go vet ./...

unformatted_files=$(gofmt -l .)
if [ -n "${unformatted_files}" ]; then
  printf 'These Go files need gofmt:\n%s\n' "${unformatted_files}" >&2
  exit 1
fi
