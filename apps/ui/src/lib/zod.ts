// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { z } from "zod";

// The dashboard's CSP has no 'unsafe-eval'. Zod probes for `new Function` support
// when the first object schema is built, and a strict CSP reports that caught probe
// as a violation on every page load. jitless skips the probe and zod's code-
// generated fast path with it; the plain parser behaves the same.
// Import `z` from here, never from "zod", so this runs before any schema exists.
z.config({ jitless: true });

export { z };
