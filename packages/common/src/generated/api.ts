export interface paths {
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
    "/api/runs/stream": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Stream run lifecycle events
         * @description Server-Sent Events stream of run creation, start, completion, failure and update events.
         */
        get: operations["streamRuns"];
        put?: never;
        post?: never;
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
    "/api/tasks/{taskName}/restart": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Restart all replicas of a service */
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
        /** Trigger a new run */
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
}
export type webhooks = Record<string, never>;
export interface components {
    schemas: {
        CapInfo: {
            available: boolean;
            name: string;
        };
        DaemonInfo: {
            /**
             * Format: uri
             * @description A URL to the JSON Schema for this object.
             * @example http://localhost:9477/schemas/DaemonInfo.json
             */
            readonly $schema?: string;
            capabilities: components["schemas"]["CapInfo"][] | null;
            cloud_enabled: boolean;
            fingerprint: string;
            /** Format: int64 */
            port: number;
            tasks: components["schemas"]["TaskBrief"][] | null;
            version: string;
        };
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
        PingEvent: Record<string, never>;
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
            /**
             * @description Why the run ended (set when status=ended)
             * @enum {string}
             */
            end_reason?: "success" | "failed" | "stopped" | "timeout" | "crashed";
            /** Format: int64 */
            exit_code: number;
            external_execution_id?: string;
            id: string;
            /** Format: int64 */
            replica_index: number;
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
            triggered_by: "cron" | "api" | "cloud";
        };
        RunCompletedEvent: {
            error?: string;
            run: components["schemas"]["Run"];
        };
        RunCreatedEvent: {
            error?: string;
            run: components["schemas"]["Run"];
        };
        RunFailedEvent: {
            error?: string;
            run: components["schemas"]["Run"];
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
            cron?: string;
            group?: string;
            /** Format: int64 */
            instances?: number;
            /** @enum {string} */
            kind?: "task" | "service";
            name: string;
            on_overlap?: string;
            /** Format: int64 */
            parallelism?: number;
            restart?: string;
        };
        TaskResponse: {
            api_trigger: boolean;
            /**
             * @description What to do when cron ticks are missed during downtime
             * @enum {string}
             */
            catch_up?: "latest" | "all" | "skip";
            cron?: string;
            description?: string;
            group?: string;
            /**
             * Format: int64
             * @description For services: number of always-running replicas
             */
            instances?: number;
            /**
             * Format: int64
             * @description Retention window, in nanoseconds
             */
            keep_for?: number;
            /** Format: int64 */
            keep_runs?: number;
            /**
             * @description Whether this is a scheduled task or an always-on service
             * @enum {string}
             */
            kind?: "task" | "service";
            /**
             * Format: int64
             * @description Per-task log size cap, in bytes
             */
            log_max_size?: number;
            /**
             * @description What to do when log output exceeds log_max_size
             * @enum {string}
             */
            log_on_full?: "drop_new" | "drop_old" | "kill_task";
            name: string;
            next_run_at?: string;
            /**
             * @description How overlapping runs are handled
             * @enum {string}
             */
            on_overlap?: "queue" | "skip" | "terminate";
            /** Format: int64 */
            parallelism?: number;
            /**
             * @description Whether and when a task is restarted after completion
             * @enum {string}
             */
            restart?: "never" | "always" | "on_failure";
            /**
             * @description Backoff curve between consecutive restarts
             * @enum {string}
             */
            restart_backoff?: "none" | "exponential";
            /**
             * Format: int64
             * @description Base delay before each restart, in nanoseconds
             */
            restart_delay?: number;
            /** Format: int64 */
            retry_attempts?: number;
            retry_backoff?: string;
            /**
             * Format: int64
             * @description Base delay before each retry, in nanoseconds
             */
            retry_delay?: number;
            /**
             * Format: int64
             * @description Per-run timeout in nanoseconds
             */
            timeout?: number;
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
    getAllRuns: {
        parameters: {
            query?: {
                /** @description Max results per page */
                limit?: number;
                /** @description Pagination offset */
                offset?: number;
                /** @description Filter by run status */
                status?: string;
                /** @description Filter by task name */
                task_name?: string;
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
    streamRuns: {
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
                /** @description Filter by run status */
                status?: string;
                /** @description Filter by task name */
                task_name?: string;
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
}

// Convenience type aliases for common response/request schemas
export type Schemas = components["schemas"];
export type Task = Schemas["TaskResponse"];
export type Run = Schemas["Run"];
export type DaemonInfo = Schemas["DaemonInfo"];
export type SystemStats = Schemas["SystemStats"];
