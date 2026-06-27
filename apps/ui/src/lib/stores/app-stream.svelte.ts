// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { SharedAppStream } from "./shared-app-stream";

/**
 * appEventStream is the app's live data feed. The daemon folds run lifecycle
 * events, periodic system samples, config-staleness flips, and in-app
 * notifications onto a single `/api/stream` SSE endpoint, so every store that
 * needs live data subscribes through this one object instead of opening its own
 * connection.
 *
 * Crucially it is cross-tab shared: across ALL open RunWisp tabs exactly one
 * (the elected leader) holds the real EventSource and rebroadcasts events to the
 * rest over a BroadcastChannel. This sidesteps the browser's per-origin
 * connection cap (~6, shared browser-wide over HTTP/1.1), which previously let a
 * handful of tabs starve each other's streams. N tabs cost one connection; when
 * the leader closes, a follower is promoted automatically. See
 * {@link SharedAppStream}.
 */
export const appEventStream = new SharedAppStream({ path: "/api/stream" });
