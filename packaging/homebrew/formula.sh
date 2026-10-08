#!/usr/bin/env bash
# Copyright (c) PoppyCake, s.r.o. SPDX-License-Identifier: GPL-3.0-or-later
#
# Print the Homebrew formula (Formula/runwisp.rb in runwisp/homebrew-tap) for a
# published release.
#
# Usage: formula.sh <version> <checksums-sha256.txt>
#   e.g. gh release download v1.2.0 -p checksums-sha256.txt
#        packaging/homebrew/formula.sh 1.2.0 checksums-sha256.txt > runwisp.rb

set -euo pipefail

version=${1:?usage: formula.sh <version> <checksums-sha256.txt>}
checksums=${2:?usage: formula.sh <version> <checksums-sha256.txt>}
base="https://github.com/runwisp/runwisp/releases/download/v${version}"

sha() {
  local sum
  sum=$(awk -v f="runwisp-$1.tar.gz" '$2 == f { print $1 }' "${checksums}")
  if [[ -z "${sum}" ]]; then
    printf 'formula.sh: no checksum for runwisp-%s.tar.gz in %s\n' "$1" "${checksums}" >&2
    exit 1
  fi
  printf '%s' "${sum}"
}

# Resolved up front: a failing $(...) inside the heredoc would not stop set -e.
sha_darwin_arm64=$(sha darwin-arm64)
sha_darwin_x64=$(sha darwin-x64)
sha_linux_arm64=$(sha linux-arm64)
sha_linux_x64=$(sha linux-x64)

cat <<RUBY
class Runwisp < Formula
  desc "Cron and process supervisor with run history, logs, web UI and TUI"
  homepage "https://runwisp.com"
  license "GPL-3.0-or-later"

  on_macos do
    if Hardware::CPU.arm?
      url "${base}/runwisp-darwin-arm64.tar.gz"
      sha256 "${sha_darwin_arm64}"
    else
      url "${base}/runwisp-darwin-x64.tar.gz"
      sha256 "${sha_darwin_x64}"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "${base}/runwisp-linux-arm64.tar.gz"
      sha256 "${sha_linux_arm64}"
    else
      url "${base}/runwisp-linux-x64.tar.gz"
      sha256 "${sha_linux_x64}"
    end
  end

  def install
    bin.install "runwisp"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/runwisp --version")
  end
end
RUBY
