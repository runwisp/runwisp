# Code review findings

This review covered daemon retention and reload behavior, SDK execution and output capture, log search, and dashboard state. The first two findings below and the PHP output issue were reproduced locally. The remaining findings were identified by tracing the code paths and have not been confirmed with race or integration tests.

## High priority

### 1. A restored soft-deleted run can still be purged

`SoftDeletePurger.purge` selects expired rows, removes their logs, then deletes rows by ID. A restore can clear `deleted_at` after selection but before `DeleteRunsByIDs`, allowing the purge to delete a run that the operator just restored. The purger should atomically claim rows for deletion, with restore respecting that claim, before removing logs. Add a regression test that restores a run after purge selection and verifies that its database row and logs remain.

Relevant code: [soft-delete purger](apps/runwisp/internal/runtime/softdelete_purger.go:60), [soft-delete queries](apps/runwisp/internal/storage/query/runs_soft_delete.sql:38).

### 2. JavaScript SDK callbacks can exceed `maxConcurrent`

The runner lets all waiters race after an execution completes. With two callbacks waiting for one freed slot, both can wake, see capacity, and start. I reproduced three simultaneous callbacks with `maxConcurrent: 2` by starting four runs and releasing one of the initial two. Reserve capacity for a waiter before waking the next one, and add a test with multiple waiters and a single freed slot.

Relevant code: [runner](packages/js/src/runner.ts:137), [runner tests](packages/js/src/runner.test.ts:88).

### 3. PHP output can corrupt UTF-8 at a chunk boundary

`Lines::cut` splits an overlong line at a fixed byte offset. When a multi-byte character straddles that offset, the resulting pieces are invalid UTF-8 and JSON encoding substitutes replacement characters. I reproduced this with 65,535 ASCII bytes followed by `€`, which was logged as replacement characters. Move the split back to a UTF-8 character boundary and add a boundary test.

Relevant code: [PHP line splitter](packages/php/src/Lines.php:60), [PHP session encoder](packages/php/src/Session.php:137), [JavaScript capture boundary handling](packages/js/src/capture.ts:59).

### 4. A stale cron probe can overwrite the state after reload

The cron watcher probes before acquiring the reconciler lock. A reload can replace the watcher while its probe is in progress, then the old watcher can apply its stale answer to the newly loaded task set. The replacement watcher remembers the newer answer and may therefore skip correcting the state on later ticks. Check that the probing watcher is still current while holding the reconciler lock before applying its answer. Add a deterministic test that pauses a probe across reload.

Relevant code: [cron watcher](apps/runwisp/internal/runtime/cronhold.go:59), [reconciler](apps/runwisp/internal/runtime/reconcile.go:196).

### 5. Log deletion failures can make retained logs unreachable

`RemoveRunLogs` does not return errors, and cleanup paths remove files before deleting their database rows. If the filesystem refuses a removal, size enforcement can still delete the row, leaving stray files with no run record. In age retention and soft-delete purge, a database deletion failure can instead leave a row whose logs have already been removed. Return and handle file-removal errors, and make row deletion retryable when owned files could not be removed. Tests should cover both filesystem and database failures.

Relevant code: [log removal helper](apps/runwisp/internal/logutil/logutil.go:59), [retention cleanup](apps/runwisp/internal/runtime/retention.go:118), [size enforcement](apps/runwisp/internal/runtime/retention.go:241), [soft-delete purge](apps/runwisp/internal/runtime/softdelete_purger.go:70).

### 6. Log-search pagination can skip runs when the run list changes

When a full 50-run window is exhausted, the cursor advances by offset. If retention deletes a run from that window before the next request, later runs shift toward the front and one can fall behind the saved offset without being searched. A newly inserted run can also shift the window and cause duplicate results. Use a stable keyset cursor based on the last run's creation time and ID. Test pagination while deleting a scanned run and while inserting a newer run.

Relevant code: [log-search cursor handling](apps/runwisp/internal/server/log_search.go:124), [run-window query](apps/runwisp/internal/server/log_search.go:150).

## Medium priority

### 7. Dashboard timezone stays stale after a timezone-only reload

The daemon applies timezone changes during reload, but the dashboard's `tasks.changed` handler refreshes only tasks. The timezone shown by the connection indicator and used for next-run labels is loaded into the system store and refreshed only on reconnect. Refresh daemon info when a reload applies, and test a timezone-only reload while the page remains open.

Relevant code: [layout event handler](apps/ui/src/routes/+layout.svelte:96), [system store](apps/ui/src/lib/stores/system.svelte.ts:112), [next-run display](apps/ui/src/lib/components/dashboard/TaskOverviewList.svelte:331).

### 8. Dashboard stores can subscribe after they were disconnected

System and notification store initialization await REST requests before subscribing. If logout or teardown calls `disconnect()` while a request is pending, the request can finish afterward and establish subscriptions again. Add an initialization generation or cancellation check and verify that resolving a pending request after disconnect leaves no subscriptions.

Relevant code: [system store initialization](apps/ui/src/lib/stores/system.svelte.ts:60), [notification store initialization](apps/ui/src/lib/stores/notifications.svelte.ts:96).

### 9. Searching later pages of one large log rereads its prefix

`ScanRun` begins at the start of the log and skips lines below the cursor's `firstN`. Repeated pages over a large run therefore reread an increasingly large prefix. Use the log index to seek to the cursor line in the current segment and skip the rotated segment when the cursor is beyond it. Test that resumed scans return the same hits while reading only the remaining portion of the log.

Relevant code: [search scan](apps/runwisp/internal/logsearch/scan.go:84), [indexed log reader](apps/runwisp/internal/logutil/log_index.go:200).

### 10. Retention logs can report runs as deleted when row deletion failed

Age/count retention logs per-task cleanup and increments `totalDeleted` before the database batch delete. If that delete fails, the daemon reports a successful cleanup count anyway and may reclaim memory even though rows remain. Have deletion return its committed outcome and base completion logs and memory reclamation on that result. Add a test using a repository that fails deletion.

Relevant code: [retention accounting](apps/runwisp/internal/runtime/retention.go:127), [batch delete and event publishing](apps/runwisp/internal/runtime/retention.go:45).
