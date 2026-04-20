// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { monotonicFactory } from "ulidx";

const monotonic = monotonicFactory();

/** Generate a monotonic ULID. Thread-safe ordering within a single process. */
export function generateUlid(): string {
  return monotonic();
}
