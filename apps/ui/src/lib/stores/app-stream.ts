// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { SharedAppStream } from "./shared-app-stream";

/**
 * appEventStream is the app's live data feed. The daemon folds run lifecycle
 * events, periodic system samples, config-staleness flips, and in-app
 * notifications onto a single `/api/events/stream` SSE endpoint, so every store
 * that needs live data subscribes through this one object instead of opening
 * its own connection. It is shared across tabs; see {@link SharedAppStream}
 * for why and how.
 */
export const appEventStream = new SharedAppStream({ path: "/api/events/stream" });
