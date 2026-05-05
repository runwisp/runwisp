// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { notificationStore, type Notification } from "./notifications.svelte";

function makeNotification(overrides: Partial<Notification> = {}): Notification {
    const base: Notification = {
        id: "01H000000000000000000NEW00",
        fingerprint: "fp",
        kind: "run.failed",
        severity: "error",
        task_name: "backup-db",
        run_id: "run-1",
        title: "backup-db failed",
        body: "exit 1",
        count: 1,
        occurrences: ["2026-05-05T12:00:00.000Z"],
        created_at: "2026-05-05T12:00:00.000Z",
        last_occurred_at: "2026-05-05T12:00:00.000Z",
    };
    return { ...base, ...overrides };
}

describe("NotificationStore lastReadAt guard", () => {
    beforeEach(() => {
        // Reset to a clean state. We don't have a public reset, but each test
        // uses unique ids so they won't collide on the byId map. The unread
        // counter is reset via _setLastReadAtForTest + an explicit floor below.
        notificationStore._setLastReadAtForTest(null);
    });

    afterEach(() => {
        notificationStore._setLastReadAtForTest(null);
    });

    it("does not bump unread when an SSE event predates the marker", () => {
        const markerTs = "2026-05-05T12:00:00.000Z";
        notificationStore._setLastReadAtForTest(markerTs);

        const before = notificationStore.unread;
        notificationStore._applyForTest(
            "notification.created",
            makeNotification({
                id: "01H000000000000000000OLD00",
                last_occurred_at: "2026-05-05T11:59:00.000Z",
            }),
        );
        expect(notificationStore.unread).toBe(before);
    });

    it("bumps unread when a new event postdates the marker", () => {
        const markerTs = "2026-05-05T12:00:00.000Z";
        notificationStore._setLastReadAtForTest(markerTs);

        const before = notificationStore.unread;
        notificationStore._applyForTest(
            "notification.created",
            makeNotification({
                id: "01H000000000000000000NEW01",
                last_occurred_at: "2026-05-05T12:01:00.000Z",
            }),
        );
        expect(notificationStore.unread).toBe(before + 1);
    });

    it("bumps unread by count delta on update when newer than marker", () => {
        const markerTs = "2026-05-05T12:00:00.000Z";
        notificationStore._setLastReadAtForTest(markerTs);

        // Seed an existing entry with the marker still in the past.
        const id = "01H000000000000000000UPD01";
        notificationStore._applyForTest(
            "notification.created",
            makeNotification({
                id,
                count: 1,
                last_occurred_at: "2026-05-05T12:01:00.000Z",
            }),
        );
        const after1 = notificationStore.unread;

        notificationStore._applyForTest(
            "notification.updated",
            makeNotification({
                id,
                count: 4,
                last_occurred_at: "2026-05-05T12:02:00.000Z",
            }),
        );
        expect(notificationStore.unread).toBe(after1 + 3);
    });

    it("does not bump on update when last_occurred_at predates marker", () => {
        const markerTs = "2026-05-05T13:00:00.000Z";
        notificationStore._setLastReadAtForTest(markerTs);

        // Seed via direct apply with a stale last_occurred_at — this also
        // shouldn't bump unread (older than marker).
        const id = "01H000000000000000000UPD02";
        notificationStore._applyForTest(
            "notification.created",
            makeNotification({
                id,
                count: 1,
                last_occurred_at: "2026-05-05T12:00:00.000Z",
            }),
        );
        const before = notificationStore.unread;

        notificationStore._applyForTest(
            "notification.updated",
            makeNotification({
                id,
                count: 5,
                last_occurred_at: "2026-05-05T12:30:00.000Z",
            }),
        );
        expect(notificationStore.unread).toBe(before);
    });
});
