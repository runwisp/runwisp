// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { toTaskPageId } from "./task-id.js";

describe("toTaskPageId", () => {
    it("prefixes the slug with task_", () => {
        expect(toTaskPageId("backup")).toBe("task_backup");
    });

    it("replaces a single non-alphanumeric character with one underscore", () => {
        expect(toTaskPageId("hello world")).toBe("task_hello_world");
    });

    it("collapses runs of non-alphanumeric characters into a single underscore", () => {
        expect(toTaskPageId("a   b")).toBe("task_a_b");
        expect(toTaskPageId("a/.-b")).toBe("task_a_b");
    });

    it("preserves digits and case", () => {
        expect(toTaskPageId("Job42")).toBe("task_Job42");
    });

    it("handles an empty task name", () => {
        expect(toTaskPageId("")).toBe("task_");
    });
});
