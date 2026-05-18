#!/usr/bin/env python3
# SPDX-FileCopyrightText: PoppyCake, s.r.o.
# SPDX-License-Identifier: Apache-2.0
"""Merge unit + e2e Go coverage profiles, keeping the max count per block."""

from collections import OrderedDict

with open(".coverage_raw.out") as fh:
    lines = fh.readlines()

blocks: "OrderedDict[str, int]" = OrderedDict()
for line in lines[1:]:
    parts = line.strip().rsplit(" ", 2)
    if len(parts) != 3:
        continue
    key = parts[0] + " " + parts[1]
    blocks[key] = max(blocks.get(key, 0), int(parts[2]))

with open("coverage.out", "w") as fh:
    fh.write(lines[0])
    fh.writelines(f"{key} {count}\n" for key, count in blocks.items())
