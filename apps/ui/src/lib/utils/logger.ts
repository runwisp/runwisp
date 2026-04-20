// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { createLoggerFactory } from "@runwisp/common";

export const createLogger = createLoggerFactory({
    enabled: true,
    prefix: "[runwisp]",
});
