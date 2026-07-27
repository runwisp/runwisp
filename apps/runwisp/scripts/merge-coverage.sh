#!/usr/bin/env sh
# SPDX-FileCopyrightText: PoppyCake, s.r.o.
# SPDX-License-Identifier: GPL-3.0-or-later
# Merge unit + e2e Go coverage profiles, keeping the max count per block.
set -eu

awk '
  NR == 1 { print; next }
  {
    key = $1 FS $2
    count = $3 + 0
    if (!(key in seen)) { seen[key] = 1; order[++n] = key; max[key] = count }
    else if (count > max[key]) { max[key] = count }
  }
  END {
    for (i = 1; i <= n; i++) print order[i], max[order[i]]
  }
' .coverage_raw.out > coverage.out
