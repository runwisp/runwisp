runner_version() {
  local version
  version=$(git describe --tags --always --dirty 2>/dev/null || printf 'dev')
  version=${version#v}
  printf '%s' "${version}"
}

runner_ldflags() {
  local module version flags
  module=$(go list -m -f '{{.Path}}')
  version=$(runner_version)
  flags="-X ${module}/internal/server.RunnerVersion=${version}"
  if [[ "${RELEASE:-}" == "1" ]]; then
    flags="-s -w ${flags}"
  fi
  printf '%s' "${flags}"
}
