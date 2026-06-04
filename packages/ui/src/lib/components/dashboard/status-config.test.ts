// Copyright (c) PoppyCake, s.r.o. SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { RUN_STATUSES } from "@runwisp/common";
import { RUN_STATUS_CONFIG } from "./status-config.js";

describe("RUN_STATUS_CONFIG", () => {
    it("has a non-empty description for every run status", () => {
        for (const status of RUN_STATUSES) {
            const config = RUN_STATUS_CONFIG[status];
            expect(config, `missing config for status "${status}"`).toBeDefined();
            expect(
                config.description.trim().length,
                `empty description for status "${status}"`,
            ).toBeGreaterThan(0);
        }
    });
});
