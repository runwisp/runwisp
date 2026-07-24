export interface paths {
    "/api/daemon/log-stream": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Stream the daemon's recent log output
         * @description Server-Sent Events stream of daemon log lines. Replays the last 100 buffered lines, then emits new lines as they're written until the client disconnects.
         */
        get: operations["streamDaemonLog"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/info": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Get daemon info */
        get: operations["getInfo"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/instance": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Get local daemon identity (loopback/socket only)
         * @description Returns the running daemon's datadir, config path, socket path, pid, version and fingerprint. Used by a second `runwisp` that hit a port conflict to discover and offer to connect to or stop this daemon. Always 403 over non-loopback TCP — the paths are local-only.
         */
        get: operations["getInstance"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/local/credentials": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Retrieve the daemon's ephemeral password (Unix socket only)
         * @description Returns the in-memory ephemeral password to a local CLI/TUI client arriving on the Unix socket. Always 403 over TCP — even with a valid JWT. Always 404 when the daemon is configured with RUNWISP_PASSWORD.
         */
        get: operations["getLocalCredentials"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/notifications": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List in-app notifications */
        get: operations["listNotifications"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/notifications/read": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Mark every unread notification read */
        post: operations["markAllNotificationsRead"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/notifications/stream": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Stream notification create/update events
         * @description Server-Sent Events stream emitting notification.created and notification.updated as in-app rows are coalesced or marked read/unread.
         */
        get: operations["streamNotifications"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/notifications/unread-count": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Count notifications with read_at IS NULL */
        get: operations["getUnreadNotificationCount"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/notifications/{id}/read": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Mark a single notification read */
        post: operations["markNotificationRead"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/notifications/{id}/unread": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Mark a single notification unread */
        post: operations["markNotificationUnread"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/reload": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Reload runwisp.toml
         * @description Re-reads the config file and reconciles the live task set (added/changed/removed). Validate-first: a config that fails to load or changes a restart-only setting is rejected and nothing is applied.
         */
        post: operations["reload"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/runs": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List all runs */
        get: operations["getAllRuns"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/runs/bulk/cancel": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Cancel every running run matched by the selector */
        post: operations["bulkCancelRuns"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/runs/bulk/delete": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Soft-delete every run matched by the selector
         * @description Marks matching terminal runs as deleted. Rows remain on disk for a short undo window before the purger reclaims them.
         */
        post: operations["bulkDeleteRuns"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/runs/bulk/rerun": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Re-run the unique tasks behind the selector's runs */
        post: operations["bulkRerunRuns"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/runs/bulk/restore": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Restore soft-deleted runs matching the selector */
        post: operations["bulkRestoreRuns"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/runs/summary": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Get aggregate run statistics */
        get: operations["getRunSummary"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/runs/{runId}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Get a single run by ID */
        get: operations["getRunById"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/stream": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Stream live application events
         * @description Single Server-Sent Events feed the web UI holds open per tab: run lifecycle events, periodic system resource samples, config-staleness flips, and in-app notifications. A client subscribes only to the event names it cares about.
         */
        get: operations["streamAppEvents"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/system": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Get system statistics */
        get: operations["getSystemStats"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/system/history": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Get historical system metrics */
        get: operations["getMetricsHistory"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/tasks": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List all tasks */
        get: operations["getTasks"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/tasks/{taskName}/log/search": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Search log lines across runs of a task
         * @description Streams on disk through the task's runs newest-first and returns matching lines. Pure on-demand scan; no index is maintained. Use `cursor` to paginate beyond the per-request hit/run budget.
         */
        get: operations["searchLogs"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/tasks/{taskName}/restart": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Restart all instances of a service */
        post: operations["restartService"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/tasks/{taskName}/run": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Trigger a new run
         * @description Triggers the task and returns the pending run immediately. Pass `wait=true` to instead hold the request open until the run finishes and return it with its exit code and end reason — a one-call alternative to triggering then polling.
         */
        post: operations["triggerRun"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/tasks/{taskName}/runs": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List runs for a task */
        get: operations["getTaskRuns"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/tasks/{taskName}/runs/{runId}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Get a single run */
        get: operations["getRun"];
        put?: never;
        post?: never;
        /** Delete a run */
        delete: operations["deleteRun"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/tasks/{taskName}/runs/{runId}/log": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Get a page of log lines
         * @description Returns a JSON page of absolute-line-numbered log entries. Use `from` (negative for tail) and `limit` to window the result.
         */
        get: operations["getLogPage"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/tasks/{taskName}/runs/{runId}/log/line/{lineNum}/history": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Get the frame history of a settled progress bar / redraw line
         * @description Returns the prior whole-region frames a progress bar or multi-line redraw passed through before settling into the committed line. Empty unless the line's `frame_count` is non-zero.
         */
        get: operations["getLogLineHistory"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/tasks/{taskName}/runs/{runId}/log/raw": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Download the run's full log as text/plain
         * @description Concatenates the rotated-away segment (`.log.prev`) and current segment so a single download captures the operator-visible byte stream.
         */
        get: operations["getLogRaw"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/tasks/{taskName}/runs/{runId}/log/stream": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Stream a run's log lines as SSE
         * @description Server-Sent Events stream of absolute-line-numbered log entries. Replays history starting at `from` (or `Last-Event-ID + 1`), then follows live output until the run terminates.
         */
        get: operations["streamLog"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/tasks/{taskName}/runs/{runId}/stop": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Stop a running task */
        post: operations["stopRun"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/tasks/{taskName}/stop": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Stop a service for the daemon's lifetime
         * @description Cancels every live instance and marks the service stopped. The supervisor stops refilling slots until a restart is issued or the daemon is restarted.
         */
        post: operations["stopService"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
}
export type webhooks = Record<string, never>;
export interface components {
    schemas: {
        BulkAffectedBody: {
            /**
             * Format: uri
             * @description A URL to the JSON Schema for this object.
             * @example http://localhost:9477/schemas/BulkAffectedBody.json
             */
            readonly $schema?: string;
            /**
             * Format: int64
             * @description Number of rows the operation touched
             */
            affected: number;
        };
        BulkRerunBody: {
            /**
             * Format: uri
             * @description A URL to the JSON Schema for this object.
             * @example http://localhost:9477/schemas/BulkRerunBody.json
             */
            readonly $schema?: string;
            /** @description New runs spawned by the rerun, keyed by task */
            triggered: components["schemas"]["TriggeredRunRef"][] | null;
        };
        CapInfo: {
            available: boolean;
            name: string;
        };
        ConfigStaleSSEEvent: {
            /** @description True when runwisp.toml changed on disk but isn't applied yet */
            stale: boolean;
        };
        DaemonInfo: {
            /**
             * Format: uri
             * @description A URL to the JSON Schema for this object.
             * @example http://localhost:9477/schemas/DaemonInfo.json
             */
            readonly $schema?: string;
            auth_disabled: boolean;
            capabilities: components["schemas"]["CapInfo"][] | null;
            cloud_enabled: boolean;
            /** Format: date-time */
            config_loaded_at: string;
            config_stale: boolean;
            external_url: string;
            fingerprint: string;
            /** Format: int64 */
            port: number;
            resolved_timezone: string;
            scheduling_active: boolean;
            service_managed: boolean;
            tasks: components["schemas"]["TaskBrief"][] | null;
            /** @enum {string} */
            timezone_source: "config" | "system";
            version: string;
        };
        DaemonLogLineEvent: {
            /** @description One captured daemon log line */
            line: string;
        };
        /**
         * @description Why a run ended. Set when status=ended.
         * @enum {string}
         */
        EndReason: "success" | "failed" | "stopped" | "timeout" | "crashed" | "skipped" | "log_overflow" | "queue_full" | "dst_skipped" | "daemon_stopped" | "missed" | "start_failed";
        ErrorDetail: {
            /** @description Where the error occurred, e.g. 'body.items[3].tags' or 'path.thing-id' */
            location?: string;
            /** @description Error message text */
            message?: string;
            /** @description The value at the given location */
            value?: unknown;
        };
        ErrorModel: {
            /**
             * Format: uri
             * @description A URL to the JSON Schema for this object.
             * @example http://localhost:9477/schemas/ErrorModel.json
             */
            readonly $schema?: string;
            /**
             * @description A human-readable explanation specific to this occurrence of the problem.
             * @example Property foo is required but is missing.
             */
            detail?: string;
            /** @description Optional list of individual error details */
            errors?: components["schemas"]["ErrorDetail"][] | null;
            /**
             * Format: uri
             * @description A URI reference that identifies the specific occurrence of the problem.
             * @example https://example.com/error-log/abc123
             */
            instance?: string;
            /**
             * Format: int64
             * @description HTTP status code
             * @example 400
             */
            status?: number;
            /**
             * @description A short, human-readable summary of the problem type. This value should not change between occurrences of the error.
             * @example Bad Request
             */
            title?: string;
            /**
             * Format: uri
             * @description A URI reference to human-readable documentation for the error.
             * @default about:blank
             * @example https://example.com/errors/example
             */
            type: string;
        };
        InstanceInfo: {
            /**
             * Format: uri
             * @description A URL to the JSON Schema for this object.
             * @example http://localhost:9477/schemas/InstanceInfo.json
             */
            readonly $schema?: string;
            /** @description Always "runwisp"; lets a caller confirm the port-holder is a RunWisp daemon. */
            app: string;
            config_path: string;
            data_dir: string;
            fingerprint: string;
            /** Format: int64 */
            pid: number;
            socket_path: string;
            version: string;
        };
        LocalCredentialsBody: {
            /**
             * Format: uri
             * @description A URL to the JSON Schema for this object.
             * @example http://localhost:9477/schemas/LocalCredentialsBody.json
             */
            readonly $schema?: string;
            /** @description Always true on success — the endpoint refuses to return env-var-supplied passwords. */
            ephemeral: boolean;
            /** @description Ephemeral password generated in memory at boot. Omitted unless ephemeral=true. */
            password: string;
        };
        LogDoneEvent: {
            /**
             * Format: int64
             * @description Last line number emitted before the run terminated
             */
            final_line: number;
            /** @description Reason the stream is closing (e.g. 'ended') */
            status: string;
        };
        LogDroppedEvent: {
            /**
             * Format: int64
             * @description Highest line number observed before drops occurred
             */
            after: number;
            /**
             * Format: int64
             * @description Number of line events dropped due to overflow
             */
            count: number;
        };
        LogLineEntry: {
            /** @description True if this segment continues an oversized split line */
            continued?: boolean;
            /**
             * Format: int64
             * @description Number of recorded prior frames if this line is a settled progress bar / redraw anchor; 0 otherwise
             */
            frame_count?: number;
            /**
             * Format: int64
             * @description Absolute line number
             */
            n: number;
            /** @description Stream identifier (stdout/stderr/system) */
            stream: string;
            /** @description Line content without trailing newline */
            text: string;
            /**
             * Format: int64
             * @description Unix milliseconds timestamp; 0 if unavailable
             */
            ts: number;
        };
        LogLineHistoryBody: {
            /**
             * Format: uri
             * @description A URL to the JSON Schema for this object.
             * @example http://localhost:9477/schemas/LogLineHistoryBody.json
             */
            readonly $schema?: string;
            /** @description Prior whole-region frames, oldest first; each frame is one or more rows */
            frames: (string[] | null)[] | null;
        };
        LogLineSSEEvent: {
            /** @description True if this segment continues an oversized split line */
            continued?: boolean;
            /**
             * Format: int64
             * @description Number of recorded prior frames if this line is a settled progress bar / redraw anchor; 0 otherwise
             */
            frame_count?: number;
            /**
             * Format: int64
             * @description Absolute line number
             */
            n: number;
            /** @description Stream identifier (stdout/stderr/system) */
            stream: string;
            /** @description Line content without trailing newline */
            text: string;
            /**
             * Format: int64
             * @description Unix milliseconds timestamp; 0 if unavailable
             */
            ts: number;
        };
        LogPageBody: {
            /**
             * Format: uri
             * @description A URL to the JSON Schema for this object.
             * @example http://localhost:9477/schemas/LogPageBody.json
             */
            readonly $schema?: string;
            /** @description True if the run has ended and the log is final */
            finalized: boolean;
            /**
             * Format: int64
             * @description Lowest line number still on disk; lines below were rotated away
             */
            first_available: number;
            /** @description Returned lines, ascending by n */
            lines: components["schemas"]["LogLineEntry"][] | null;
            /**
             * Format: int64
             * @description Total lines produced across all segments
             */
            total_lines: number;
            /** @description True if rotation has dropped lines below first_available */
            truncated: boolean;
        };
        LogRegionSSEEvent: {
            /**
             * Format: int64
             * @description Region generation; bumps on screen reset so stale frames can be discarded
             */
            epoch: number;
            /** @description Current frame of the region, one entry per row; empty clears the overlay */
            rows: string[] | null;
            /** @description Stream identifier (stdout/stderr) */
            stream: string;
        };
        LogRotatedEvent: {
            /**
             * Format: int64
             * @description Lowest line number still on disk after rotation
             */
            first_available: number;
        };
        LogSearchBody: {
            /**
             * Format: uri
             * @description A URL to the JSON Schema for this object.
             * @example http://localhost:9477/schemas/LogSearchBody.json
             */
            readonly $schema?: string;
            /** @description True when no further runs / lines need scanning */
            exhausted: boolean;
            /** @description Hits ordered newest-run-first, then ascending line within a run */
            hits: components["schemas"]["LogSearchHit"][] | null;
            /** @description Opaque token to fetch the next page; empty when the scan is exhausted */
            next_cursor?: string;
            /**
             * Format: int64
             * @description Number of runs visited by this request
             */
            scanned_runs: number;
        };
        LogSearchHit: {
            /**
             * Format: int64
             * @description Absolute line number within the run
             */
            n: number;
            /** @description ULID of the run containing this line */
            run_id: string;
            /** @description Stream identifier (stdout/stderr/system) */
            stream: string;
            /** @description Matched line content without trailing newline */
            text: string;
            /**
             * Format: int64
             * @description Run created_at in Unix milliseconds (used for newest-first sort)
             */
            ts: number;
        };
        MetricsSample: {
            /**
             * Format: double
             * @description CPU usage percentage (0-100)
             */
            cpu: number;
            /**
             * Format: double
             * @description Memory usage percentage (0-100)
             */
            mem: number;
            /**
             * Format: int64
             * @description Total memory in bytes
             */
            mem_total: number;
            /**
             * Format: int64
             * @description Used memory in bytes
             */
            mem_used: number;
            /**
             * Format: int64
             * @description Unix timestamp (seconds)
             */
            ts: number;
        };
        NotificationCreatedEvent: {
            notification: components["schemas"]["NotificationDTO"];
            /** Format: int64 */
            unread_count: number;
        };
        NotificationDTO: {
            /** @description Pre-rendered body text */
            body: string;
            /**
             * Format: int64
             * @description Number of coalesced occurrences within the window
             */
            count: number;
            /**
             * Format: date-time
             * @description First time this notification was raised
             */
            created_at: string;
            /** @description Coalescing key (FNV1a hex) */
            fingerprint: string;
            /** @description Stable ULID identifier */
            id: string;
            /** @description Event kind (run.failed, notify.delivery_failed, ...) */
            kind: string;
            /**
             * Format: date-time
             * @description Most recent occurrence
             */
            last_occurred_at: string;
            /** @description Most-recent timestamps (newest first), ISO8601 */
            occurrences: string[] | null;
            /**
             * Format: date-time
             * @description When the operator marked this row read; null/absent when unread
             */
            read_at?: string;
            /** @description Run that produced this notification (empty when not run-derived) */
            run_id: string;
            /** @description info | warn | error */
            severity: string;
            /** @description Task that produced this notification (empty for daemon-level events) */
            task_name: string;
            /** @description Human-readable title */
            title: string;
        };
        NotificationUnreadBody: {
            /**
             * Format: uri
             * @description A URL to the JSON Schema for this object.
             * @example http://localhost:9477/schemas/NotificationUnreadBody.json
             */
            readonly $schema?: string;
            /**
             * Format: int64
             * @description Number of notifications with read_at IS NULL
             */
            count: number;
        };
        NotificationUnreadCountEvent: {
            /** Format: int64 */
            unread_count: number;
        };
        NotificationUpdatedEvent: {
            notification: components["schemas"]["NotificationDTO"];
            /** Format: int64 */
            unread_count: number;
        };
        NotificationsListBody: {
            /**
             * Format: uri
             * @description A URL to the JSON Schema for this object.
             * @example http://localhost:9477/schemas/NotificationsListBody.json
             */
            readonly $schema?: string;
            /** @description Notifications in id-DESC order */
            items: components["schemas"]["NotificationDTO"][] | null;
            /** @description Cursor to pass as 'before' on the next page; empty when exhausted */
            next_cursor?: string;
        };
        PingEvent: Record<string, never>;
        ReloadResult: {
            /**
             * Format: uri
             * @description A URL to the JSON Schema for this object.
             * @example http://localhost:9477/schemas/ReloadResult.json
             */
            readonly $schema?: string;
            /** @description Names of tasks added by the reload */
            added: string[] | null;
            /** @description Tasks whose definition changed, with the reasons */
            changed: components["schemas"]["ReloadTaskChange"][] | null;
            /** @description Names of tasks removed by the reload */
            removed: string[] | null;
        };
        ReloadTaskChange: {
            /** @description Task name */
            name: string;
            /** @description Why the task is considered changed */
            reasons: string[] | null;
        };
        Run: {
            /**
             * Format: uri
             * @description A URL to the JSON Schema for this object.
             * @example http://localhost:9477/schemas/Run.json
             */
            readonly $schema?: string;
            /** Format: date-time */
            created_at: string;
            /** Format: date-time */
            end_at?: string;
            end_reason?: components["schemas"]["EndReason"];
            /** Format: int64 */
            exit_code: number;
            external_execution_id?: string;
            id: string;
            /** Format: int64 */
            instance_index: number;
            params?: {
                [key: string]: string;
            };
            /** Format: int64 */
            retry_attempt: number;
            retry_of_run_id?: string;
            /** Format: date-time */
            start_at?: string;
            /**
             * @description Run lifecycle phase
             * @enum {string}
             */
            status: "pending" | "running" | "ended";
            task_name: string;
            /**
             * @description How the run was triggered
             * @enum {string}
             */
            triggered_by: "cron" | "api" | "cloud" | "service" | "startup";
        };
        RunCompletedEvent: {
            error?: string;
            run: components["schemas"]["Run"];
        };
        RunCreatedEvent: {
            error?: string;
            run: components["schemas"]["Run"];
        };
        RunDeletedSSEEvent: {
            run_id: string;
            task_name: string;
        };
        RunFailedEvent: {
            error?: string;
            run: components["schemas"]["Run"];
        };
        RunFilter: {
            /**
             * Format: date-time
             * @description Only runs created at or after this time
             */
            created_after?: string;
            /**
             * Format: date-time
             * @description Only runs created at or before this time
             */
            created_before?: string;
            /**
             * Format: int64
             * @description Only runs whose exit code is <= this (inclusive)
             */
            exit_code_max?: number;
            /**
             * Format: int64
             * @description Only runs whose exit code is >= this (inclusive)
             */
            exit_code_min?: number;
            /** @description Only runs that are a retry (retry_attempt > 0) */
            retries_only?: boolean;
            /** @description Search query against task_name / id */
            search?: string;
            /** @description Comma-separated run statuses (phase or end reason); a run matches any listed value */
            status?: string;
            /** @description Filter by task name */
            task_name?: string;
            /** @description Filter by what triggered the run (cron/api/cloud/service/startup) */
            triggered_by?: string;
        };
        RunSelector: {
            /**
             * Format: uri
             * @description A URL to the JSON Schema for this object.
             * @example http://localhost:9477/schemas/RunSelector.json
             */
            readonly $schema?: string;
            /** @description IDs to exclude when MatchAll is true */
            except_ids?: string[] | null;
            /** @description Filter to apply when MatchAll is true */
            filter?: components["schemas"]["RunFilter"];
            /** @description Explicit run IDs to select when MatchAll is false */
            ids?: string[] | null;
            /** @description When true, selects every run matching Filter except those listed in ExceptIDs */
            match_all?: boolean;
        };
        RunStartedEvent: {
            error?: string;
            run: components["schemas"]["Run"];
        };
        RunSummary: {
            /**
             * Format: uri
             * @description A URL to the JSON Schema for this object.
             * @example http://localhost:9477/schemas/RunSummary.json
             */
            readonly $schema?: string;
            /**
             * Format: int64
             * @description Number of failed runs
             */
            failed: number;
            /**
             * Format: date-time
             * @description Timestamp of most recent failure
             */
            last_failure?: string;
            /**
             * Format: int64
             * @description Number of scheduled runs missed during downtime
             */
            missed: number;
            /**
             * Format: int64
             * @description Number of successful runs
             */
            success: number;
            /**
             * Format: int64
             * @description Total number of runs
             */
            total: number;
        };
        RunUpdatedEvent: {
            error?: string;
            run: components["schemas"]["Run"];
        };
        RunsResponseBody: {
            /**
             * Format: uri
             * @description A URL to the JSON Schema for this object.
             * @example http://localhost:9477/schemas/RunsResponseBody.json
             */
            readonly $schema?: string;
            /** @description List of runs */
            runs: components["schemas"]["Run"][] | null;
            /**
             * Format: int64
             * @description Total matching runs
             */
            total: number;
        };
        StopRunOutputBody: {
            /**
             * Format: uri
             * @description A URL to the JSON Schema for this object.
             * @example http://localhost:9477/schemas/StopRunOutputBody.json
             */
            readonly $schema?: string;
            /** @description Result message */
            message: string;
        };
        SystemSampleSSEEvent: {
            /** @description Resource snapshot, same shape as a metrics-history entry */
            sample: components["schemas"]["MetricsSample"];
            /** @description Human-readable daemon uptime */
            uptime: string;
        };
        SystemStats: {
            /**
             * Format: uri
             * @description A URL to the JSON Schema for this object.
             * @example http://localhost:9477/schemas/SystemStats.json
             */
            readonly $schema?: string;
            /** @description CPU architecture (e.g. amd64, arm64) */
            arch: string;
            /**
             * Format: int64
             * @description Number of CPU cores
             */
            cpu_cores: number;
            /**
             * Format: double
             * @description CPU usage percentage (0-100)
             */
            cpu_usage: number;
            /** @description Hostname */
            host: string;
            /**
             * Format: int64
             * @description Total memory in bytes
             */
            mem_total: number;
            /**
             * Format: double
             * @description Memory usage percentage (0-100)
             */
            mem_usage: number;
            /**
             * Format: int64
             * @description Used memory in bytes
             */
            mem_used: number;
            /** @description Application name */
            name: string;
            /** @description Operating system (e.g. linux, darwin, windows) */
            os: string;
            /** @description Human-readable uptime */
            uptime: string;
            /** @description RunWisp version */
            version: string;
            /** @description Working directory of the daemon process */
            work_dir: string;
        };
        TaskBrief: {
            api_trigger: boolean;
            catch_up?: string;
            compose?: components["schemas"]["TaskComposeRef"];
            cron?: string;
            depends_on?: string[] | null;
            group?: string;
            /** Format: int64 */
            instances?: number;
            /** @enum {string} */
            kind?: "task" | "service";
            /** Format: int64 */
            max_concurrent?: number;
            name: string;
            on_overlap?: string;
            parameters?: components["schemas"]["TaskParam"][] | null;
            restart?: string;
            staged?: boolean;
        };
        TaskComposeRef: {
            file: string;
            project_name: string;
            service?: string;
        };
        TaskParam: {
            /** @description When choices is set, allow values outside the list */
            allow_custom?: boolean;
            /** @description Allowed values; renders as a dropdown */
            choices?: string[] | null;
            /** @description Default value used by scheduled runs and pre-filled in manual forms */
            default?: string;
            /** @description Help text shown under the field */
            description?: string;
            /** @description Canonical parameter key (env name, positional label, or option/flag token) */
            key: string;
            /**
             * @description How the parameter renders into the run
             * @enum {string}
             */
            kind: "env" | "arg" | "option" | "flag";
            /** @description Whether a manual trigger must supply a value */
            required?: boolean;
            /**
             * @description Value type; defaults to string
             * @enum {string}
             */
            type?: "string" | "number";
        };
        TaskResponse: {
            api_trigger: boolean;
            /** @description For services: whether instances start at boot. False boots in the stopped state until started via API/UI. */
            autostart: boolean;
            /**
             * @description What to do when cron ticks are missed during downtime
             * @enum {string}
             */
            catch_up?: "latest" | "all" | "skip";
            /** @description Provenance metadata for tasks imported from a docker compose file */
            compose?: components["schemas"]["TaskComposeRef"];
            cron?: string;
            /** @description For services: service names that must be healthy before this one starts at boot — boot ordering only, not a workflow DAG */
            depends_on?: string[] | null;
            description?: string;
            /** @description Environment variables overlaid on the task's process env. Values are visible to authenticated operators in the API/UI; env_file values merge in beneath the inline entries. */
            env?: {
                [key: string]: string;
            };
            /** @description Path to a dotenv file whose KEY=VALUE pairs merge into env (inline entries win). Values are visible in the API/UI like inline env. */
            env_file?: string;
            /** @description Process exit codes treated as success; defaults to [0] */
            exit_codes?: number[] | null;
            /**
             * Format: int64
             * @description Window between the stop signal and SIGKILL when a run is stopped, in nanoseconds
             */
            graceful_stop?: number;
            group?: string;
            /**
             * Format: int64
             * @description For services: an instance that runs at least this long counts as healthy — resets the restart counter and clears the failed-start streak; fast exits below it count toward start_retries, in nanoseconds
             */
            healthy_after?: number;
            /**
             * Format: int64
             * @description For services: number of always-running instances
             */
            instances?: number;
            /**
             * Format: int64
             * @description Cap how far a cron task's start may slip so tasks sharing a fire time take turns through a daemon-wide one-at-a-time gate instead of stampeding; a run starts as soon as the gate frees and slips up to this window only under contention, in nanoseconds
             */
            jitter?: number;
            /**
             * Format: int64
             * @description Retention window in nanoseconds; 0 means no cap was configured
             */
            keep_for?: number;
            /**
             * Format: int64
             * @description Row-count retention cap; 0 means no cap was configured
             */
            keep_runs?: number;
            /**
             * @description Whether this is a scheduled task or an always-on service
             * @enum {string}
             */
            kind?: "task" | "service";
            /**
             * Format: int64
             * @description Per-run log size cap in bytes
             */
            log_max_size?: number;
            /**
             * @description What to do when log output exceeds log_max_size
             * @enum {string}
             */
            log_on_full?: "drop_new" | "drop_old" | "kill_task";
            /**
             * Format: int64
             * @description Cap on catch-up runs triggered when catch_up = all
             */
            max_catch_up_runs?: number;
            /**
             * Format: int64
             * @description Maximum overlapping runs allowed for this task
             */
            max_concurrent?: number;
            name: string;
            next_run_at?: string;
            /**
             * @description How overlapping runs are handled
             * @enum {string}
             */
            on_overlap?: "queue" | "skip" | "terminate";
            /** @description Per-execution parameters an operator may supply at manual trigger time; scheduled runs use the declared defaults */
            parameters?: components["schemas"]["TaskParam"][] | null;
            /**
             * Format: int64
             * @description For services: boot start order, lowest first (name breaks ties). Start order only — not a dependency.
             */
            priority?: number;
            /**
             * Format: int64
             * @description Maximum runs that can wait when on_overlap = queue
             */
            queue_max?: number;
            /**
             * @description Whether and when a task is restarted after completion
             * @enum {string}
             */
            restart?: "never" | "always" | "on_failure";
            /**
             * @description Backoff curve between consecutive restarts
             * @enum {string}
             */
            restart_backoff?: "constant" | "linear" | "exponential";
            /**
             * Format: int64
             * @description Base delay before each restart, in nanoseconds
             */
            restart_delay?: number;
            /** Format: int64 */
            retry_attempts?: number;
            /**
             * @description Backoff curve between consecutive retries
             * @enum {string}
             */
            retry_backoff?: "constant" | "linear" | "exponential";
            /**
             * Format: int64
             * @description Base delay before each retry, in nanoseconds
             */
            retry_delay?: number;
            /** @description For tasks: fire once at daemon startup, in addition to any cron schedule */
            run_on_start?: boolean;
            /** @description Path to a dotenv file whose KEY=VALUE pairs are injected into the task's process env. The path is visible in the API/UI; keys and values never leave the daemon. */
            secrets_file?: string;
            /** @description Absolute path to the shell interpreter for run scripts; defaults to /bin/sh */
            shell?: string;
            /** @description True when the task's definition lives in the machine-owned staging file — imported, not yet promoted to native TOML */
            staged?: boolean;
            /**
             * Format: int64
             * @description For services: consecutive fast failures tolerated before an instance is marked FATAL and stops restarting
             */
            start_retries?: number;
            /**
             * @description Signal sent to stop a run before SIGKILL; defaults to SIGTERM
             * @enum {string}
             */
            stop_signal?: "SIGTERM" | "SIGINT" | "SIGQUIT" | "SIGHUP" | "SIGKILL" | "SIGUSR1" | "SIGUSR2";
            /**
             * Format: int64
             * @description Per-run timeout in nanoseconds
             */
            timeout?: number;
            /** @description IANA timezone for cron evaluation; falls back to scheduler.timezone, then the daemon's resolved system timezone */
            timezone?: string;
            /** @description Octal file-creation mask applied to the run's process; empty inherits the daemon's umask */
            umask?: string;
            /** @description Run the process as this OS user, in 'user' or 'user:group' form (name or numeric id). Empty runs as the daemon's user; switching users needs the daemon running as root. */
            user?: string;
            /** @description Resolved working directory for the task's process; empty inherits the daemon's working directory */
            working_dir?: string;
        };
        TriggerRunInputBody: {
            /**
             * Format: uri
             * @description A URL to the JSON Schema for this object.
             * @example http://localhost:9477/schemas/TriggerRunInputBody.json
             */
            readonly $schema?: string;
            /** @description Values for the task's declared parameters, keyed by parameter identity. A null value omits that parameter (overriding its default); an empty string passes an empty value; an absent key uses the declared default. */
            params?: {
                [key: string]: string | null;
            };
        };
        TriggeredRunRef: {
            run_id: string;
            task_name: string;
        };
    };
    responses: never;
    parameters: never;
    requestBodies: never;
    headers: never;
    pathItems: never;
}
export type $defs = Record<string, never>;
export interface operations {
    streamDaemonLog: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "text/event-stream": {
                        data: components["schemas"]["DaemonLogLineEvent"];
                        /**
                         * @description The event name.
                         * @constant
                         */
                        event: "line";
                        /** @description The event ID. */
                        id?: number;
                        /** @description The retry time in milliseconds. */
                        retry?: number;
                    }[];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    getInfo: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["DaemonInfo"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    getInstance: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["InstanceInfo"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    getLocalCredentials: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    "Cache-Control"?: string;
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["LocalCredentialsBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    listNotifications: {
        parameters: {
            query?: {
                /** @description Max items per page */
                limit?: number;
                /** @description Cursor: return only items with id < before (descending) */
                before?: string;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["NotificationsListBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    markAllNotificationsRead: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description No Content */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    streamNotifications: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "text/event-stream": ({
                        data: components["schemas"]["NotificationCreatedEvent"];
                        /**
                         * @description The event name.
                         * @constant
                         */
                        event: "notification.created";
                        /** @description The event ID. */
                        id?: number;
                        /** @description The retry time in milliseconds. */
                        retry?: number;
                    } | {
                        data: components["schemas"]["NotificationUpdatedEvent"];
                        /**
                         * @description The event name.
                         * @constant
                         */
                        event: "notification.updated";
                        /** @description The event ID. */
                        id?: number;
                        /** @description The retry time in milliseconds. */
                        retry?: number;
                    } | {
                        data: components["schemas"]["NotificationUnreadCountEvent"];
                        /**
                         * @description The event name.
                         * @constant
                         */
                        event: "notifications.unread_count_changed";
                        /** @description The event ID. */
                        id?: number;
                        /** @description The retry time in milliseconds. */
                        retry?: number;
                    } | {
                        data: components["schemas"]["PingEvent"];
                        /**
                         * @description The event name.
                         * @constant
                         */
                        event: "ping";
                        /** @description The event ID. */
                        id?: number;
                        /** @description The retry time in milliseconds. */
                        retry?: number;
                    })[];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    getUnreadNotificationCount: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["NotificationUnreadBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    markNotificationRead: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description Notification ULID */
                id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description No Content */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    markNotificationUnread: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description Notification ULID */
                id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description No Content */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    reload: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ReloadResult"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    getAllRuns: {
        parameters: {
            query?: {
                /** @description Max results per page */
                limit?: number;
                /** @description Pagination offset */
                offset?: number;
                /** @description Comma-separated run statuses (phase or end reason); a run matches any listed value */
                status?: string;
                /** @description Filter by task name */
                task_name?: string;
                /** @description Filter by what triggered the run */
                triggered_by?: "cron" | "api" | "cloud" | "service" | "startup" | "";
                /** @description Only runs created at or after this RFC3339 time */
                created_after?: string;
                /** @description Only runs created at or before this RFC3339 time */
                created_before?: string;
                /** @description Only runs whose exit code is >= this (inclusive) */
                exit_code_min?: string;
                /** @description Only runs whose exit code is <= this (inclusive) */
                exit_code_max?: string;
                /** @description Only runs that are a retry (retry_attempt > 0) */
                retries_only?: boolean;
                /** @description Field to sort by */
                sort_field?: "task_name" | "status" | "start_at" | "exit_code" | "duration" | "created_at" | "";
                /** @description Sort direction */
                sort_direction?: "asc" | "desc" | "";
                /** @description Search query */
                search?: string;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["RunsResponseBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    bulkCancelRuns: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["RunSelector"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["BulkAffectedBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    bulkDeleteRuns: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["RunSelector"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["BulkAffectedBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    bulkRerunRuns: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["RunSelector"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["BulkRerunBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    bulkRestoreRuns: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["RunSelector"];
            };
        };
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["BulkAffectedBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    getRunSummary: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["RunSummary"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    getRunById: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description Run ULID */
                runId: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Run"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    streamAppEvents: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "text/event-stream": ({
                        data: components["schemas"]["ConfigStaleSSEEvent"];
                        /**
                         * @description The event name.
                         * @constant
                         */
                        event: "config.stale";
                        /** @description The event ID. */
                        id?: number;
                        /** @description The retry time in milliseconds. */
                        retry?: number;
                    } | {
                        data: components["schemas"]["NotificationCreatedEvent"];
                        /**
                         * @description The event name.
                         * @constant
                         */
                        event: "notification.created";
                        /** @description The event ID. */
                        id?: number;
                        /** @description The retry time in milliseconds. */
                        retry?: number;
                    } | {
                        data: components["schemas"]["NotificationUpdatedEvent"];
                        /**
                         * @description The event name.
                         * @constant
                         */
                        event: "notification.updated";
                        /** @description The event ID. */
                        id?: number;
                        /** @description The retry time in milliseconds. */
                        retry?: number;
                    } | {
                        data: components["schemas"]["NotificationUnreadCountEvent"];
                        /**
                         * @description The event name.
                         * @constant
                         */
                        event: "notifications.unread_count_changed";
                        /** @description The event ID. */
                        id?: number;
                        /** @description The retry time in milliseconds. */
                        retry?: number;
                    } | {
                        data: components["schemas"]["PingEvent"];
                        /**
                         * @description The event name.
                         * @constant
                         */
                        event: "ping";
                        /** @description The event ID. */
                        id?: number;
                        /** @description The retry time in milliseconds. */
                        retry?: number;
                    } | {
                        data: components["schemas"]["RunCompletedEvent"];
                        /**
                         * @description The event name.
                         * @constant
                         */
                        event: "run.completed";
                        /** @description The event ID. */
                        id?: number;
                        /** @description The retry time in milliseconds. */
                        retry?: number;
                    } | {
                        data: components["schemas"]["RunCreatedEvent"];
                        /**
                         * @description The event name.
                         * @constant
                         */
                        event: "run.created";
                        /** @description The event ID. */
                        id?: number;
                        /** @description The retry time in milliseconds. */
                        retry?: number;
                    } | {
                        data: components["schemas"]["RunDeletedSSEEvent"];
                        /**
                         * @description The event name.
                         * @constant
                         */
                        event: "run.deleted";
                        /** @description The event ID. */
                        id?: number;
                        /** @description The retry time in milliseconds. */
                        retry?: number;
                    } | {
                        data: components["schemas"]["RunFailedEvent"];
                        /**
                         * @description The event name.
                         * @constant
                         */
                        event: "run.failed";
                        /** @description The event ID. */
                        id?: number;
                        /** @description The retry time in milliseconds. */
                        retry?: number;
                    } | {
                        data: components["schemas"]["RunStartedEvent"];
                        /**
                         * @description The event name.
                         * @constant
                         */
                        event: "run.started";
                        /** @description The event ID. */
                        id?: number;
                        /** @description The retry time in milliseconds. */
                        retry?: number;
                    } | {
                        data: components["schemas"]["RunUpdatedEvent"];
                        /**
                         * @description The event name.
                         * @constant
                         */
                        event: "run.updated";
                        /** @description The event ID. */
                        id?: number;
                        /** @description The retry time in milliseconds. */
                        retry?: number;
                    } | {
                        data: components["schemas"]["SystemSampleSSEEvent"];
                        /**
                         * @description The event name.
                         * @constant
                         */
                        event: "system";
                        /** @description The event ID. */
                        id?: number;
                        /** @description The retry time in milliseconds. */
                        retry?: number;
                    })[];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    getSystemStats: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["SystemStats"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    getMetricsHistory: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["MetricsSample"][] | null;
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    getTasks: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["TaskResponse"][] | null;
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    searchLogs: {
        parameters: {
            query?: {
                /** @description Restrict the search to one run (ULID). Empty searches every non-deleted run of the task. */
                run_id?: string;
                /** @description Substring (or regex when regex=true) to search for */
                q?: string;
                /** @description Treat q as an RE2 regular expression */
                regex?: boolean;
                /** @description Match case-sensitively (default is case-insensitive) */
                case?: boolean;
                /** @description Max hits returned (default 200) */
                limit?: number;
                /** @description Opaque continuation token returned by a previous call */
                cursor?: string;
            };
            header?: never;
            path: {
                /** @description Task name */
                taskName: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["LogSearchBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    restartService: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description Task name */
                taskName: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["StopRunOutputBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    triggerRun: {
        parameters: {
            query?: {
                /** @description Block until the run finishes and return the completed run (with exit_code and end_reason). Best for short tasks; long runs may exceed reverse-proxy timeouts — follow the log stream or poll instead. */
                wait?: boolean;
                /** @description With wait=true, the maximum seconds to hold the request open. On timeout the run keeps running and the response returns it in its current (non-terminal) state. */
                wait_timeout?: number;
            };
            header?: never;
            path: {
                /** @description Task name */
                taskName: string;
            };
            cookie?: never;
        };
        requestBody?: {
            content: {
                "application/json": components["schemas"]["TriggerRunInputBody"];
            };
        };
        responses: {
            /** @description Created */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Run"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    getTaskRuns: {
        parameters: {
            query?: {
                /** @description Max results per page */
                limit?: number;
                /** @description Pagination offset */
                offset?: number;
                /** @description Comma-separated run statuses (phase or end reason); a run matches any listed value */
                status?: string;
                /** @description Filter by task name */
                task_name?: string;
                /** @description Filter by what triggered the run */
                triggered_by?: "cron" | "api" | "cloud" | "service" | "startup" | "";
                /** @description Only runs created at or after this RFC3339 time */
                created_after?: string;
                /** @description Only runs created at or before this RFC3339 time */
                created_before?: string;
                /** @description Only runs whose exit code is >= this (inclusive) */
                exit_code_min?: string;
                /** @description Only runs whose exit code is <= this (inclusive) */
                exit_code_max?: string;
                /** @description Only runs that are a retry (retry_attempt > 0) */
                retries_only?: boolean;
                /** @description Field to sort by */
                sort_field?: "task_name" | "status" | "start_at" | "exit_code" | "duration" | "created_at" | "";
                /** @description Sort direction */
                sort_direction?: "asc" | "desc" | "";
                /** @description Search query */
                search?: string;
            };
            header?: never;
            path: {
                /** @description Task name */
                taskName: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["RunsResponseBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    getRun: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description Task name */
                taskName: string;
                /** @description Run ULID */
                runId: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Run"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    deleteRun: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description Task name */
                taskName: string;
                /** @description Run ULID */
                runId: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description No Content */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    getLogPage: {
        parameters: {
            query?: {
                /** @description Anchor line number; 0 is the first line, negative values count from end (default -1000) */
                from?: number;
                /** @description Max lines returned (default 1000) */
                limit?: number;
            };
            header?: never;
            path: {
                /** @description Task name */
                taskName: string;
                /** @description Run ULID */
                runId: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["LogPageBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    getLogLineHistory: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description Task name */
                taskName: string;
                /** @description Run ULID */
                runId: string;
                /** @description Anchor line number to fetch frame history for */
                lineNum: number;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["LogLineHistoryBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    getLogRaw: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description Task name */
                taskName: string;
                /** @description Run ULID */
                runId: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    "Content-Type"?: string;
                    [name: string]: unknown;
                };
                content: {
                    "application/json": string;
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    streamLog: {
        parameters: {
            query?: {
                /** @description Anchor line number; 0 is the first line, negative values count from end (default -1000) */
                from?: number;
                /** @description Cap on backfilled lines (default 5000) */
                replay_limit?: number;
            };
            header?: {
                /** @description Native SSE resume cursor; takes precedence over the from query */
                "Last-Event-ID"?: string;
            };
            path: {
                /** @description Task name */
                taskName: string;
                /** @description Run ULID */
                runId: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "text/event-stream": ({
                        data: components["schemas"]["LogDoneEvent"];
                        /**
                         * @description The event name.
                         * @constant
                         */
                        event: "done";
                        /** @description The event ID. */
                        id?: number;
                        /** @description The retry time in milliseconds. */
                        retry?: number;
                    } | {
                        data: components["schemas"]["LogDroppedEvent"];
                        /**
                         * @description The event name.
                         * @constant
                         */
                        event: "dropped";
                        /** @description The event ID. */
                        id?: number;
                        /** @description The retry time in milliseconds. */
                        retry?: number;
                    } | {
                        data: components["schemas"]["LogLineSSEEvent"];
                        /**
                         * @description The event name.
                         * @constant
                         */
                        event: "line";
                        /** @description The event ID. */
                        id?: number;
                        /** @description The retry time in milliseconds. */
                        retry?: number;
                    } | {
                        data: components["schemas"]["LogRegionSSEEvent"];
                        /**
                         * @description The event name.
                         * @constant
                         */
                        event: "region";
                        /** @description The event ID. */
                        id?: number;
                        /** @description The retry time in milliseconds. */
                        retry?: number;
                    } | {
                        data: components["schemas"]["LogRotatedEvent"];
                        /**
                         * @description The event name.
                         * @constant
                         */
                        event: "rotated";
                        /** @description The event ID. */
                        id?: number;
                        /** @description The retry time in milliseconds. */
                        retry?: number;
                    })[];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    stopRun: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description Task name */
                taskName: string;
                /** @description Run ULID */
                runId: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["StopRunOutputBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
    stopService: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description Task name */
                taskName: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["StopRunOutputBody"];
                };
            };
            /** @description Error */
            default: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["ErrorModel"];
                };
            };
        };
    };
}

// Convenience type aliases for common response/request schemas
export type Schemas = components["schemas"];
export type Task = Schemas["TaskResponse"];
export type Run = Schemas["Run"];
export type DaemonInfo = Schemas["DaemonInfo"];
export type SystemStats = Schemas["SystemStats"];
