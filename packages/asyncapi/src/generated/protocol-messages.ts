// Generated from asyncapi.yaml
import { z } from "zod";

export const PROTOCOL_VERSION = 2;

export const inboundDaemonMessageSchema = z.discriminatedUnion("type", [
  z.object({ "type": z.literal("ping"), "v": z.literal(PROTOCOL_VERSION).optional(), "sentAt": z.string().optional(), "id": z.string().optional(), "systemStats": z.object({ "cpuUsage": z.number().optional(), "memUsage": z.number().optional(), "memTotal": z.number().int().optional(), "memUsed": z.number().int().optional(), "cpuCores": z.number().int().optional(), "uptime": z.string().optional(), "version": z.string().optional(), "host": z.string().optional(), "os": z.string().optional(), "arch": z.string().optional() }).optional() }),
  z.object({ "type": z.literal("execution:ack"), "v": z.literal(PROTOCOL_VERSION).optional(), "sentAt": z.string().optional(), "executionId": z.string() }),
  z.object({ "type": z.literal("execution:update"), "v": z.literal(PROTOCOL_VERSION).optional(), "sentAt": z.string().optional(), "executionId": z.string(), "status": z.enum(["running", "ok", "err", "stopped", "timeout"]), "exitCode": z.number().int().nullable().optional(), "startedAt": z.coerce.date().nullable().optional(), "finishedAt": z.coerce.date().nullable().optional(), "logPath": z.string().nullable().optional(), "logSize": z.number().int().nonnegative().nullable().optional() }),
  z.object({ "type": z.literal("log:line"), "v": z.literal(PROTOCOL_VERSION).optional(), "sentAt": z.string().optional(), "executionId": z.string(), "n": z.number().int(), "ts": z.number().int().optional(), "stream": z.enum(["stdout", "stderr", "system"]), "text": z.string(), "continued": z.boolean().optional() }),
  z.object({ "type": z.literal("log:replayChunk"), "v": z.literal(PROTOCOL_VERSION).optional(), "sentAt": z.string().optional(), "id": z.string(), "executionId": z.string(), "lines": z.unknown(), "final": z.boolean() }),
  z.object({ "type": z.literal("log:searchChunk"), "v": z.literal(PROTOCOL_VERSION).optional(), "sentAt": z.string().optional(), "id": z.string(), "executionId": z.string(), "hits": z.unknown(), "nextLine": z.number().int().optional(), "exhausted": z.boolean() }),
  z.object({ "type": z.literal("service:status"), "v": z.literal(PROTOCOL_VERSION).optional(), "sentAt": z.string().optional(), "taskId": z.string(), "state": z.enum(["running", "degraded", "stopped", "fatal"]), "desiredInstances": z.number().int(), "runningInstances": z.number().int(), "instances": z.unknown() }),
  z.object({ "type": z.literal("error"), "v": z.literal(PROTOCOL_VERSION).optional(), "sentAt": z.string().optional(), "code": z.string(), "message": z.string(), "requestId": z.string().optional(), "executionId": z.string().optional() })
]);

export type InboundDaemonMessage = z.infer<typeof inboundDaemonMessageSchema>;

export const outboundDaemonMessageSchema = z.discriminatedUnion("type", [
  z.object({ "type": z.literal("auth:result"), "v": z.literal(PROTOCOL_VERSION).optional(), "sentAt": z.string().optional(), "success": z.boolean(), "connectionId": z.string().optional(), "error": z.string().optional() }),
  z.object({ "type": z.literal("pong"), "v": z.literal(PROTOCOL_VERSION).optional(), "sentAt": z.string().optional() }),
  z.object({ "type": z.literal("execution:dispatch"), "v": z.literal(PROTOCOL_VERSION).optional(), "sentAt": z.string().optional(), "execution": z.object({ "id": z.string(), "taskId": z.string(), "taskName": z.string(), "executionId": z.string(), "priority": z.number().int(), "triggeredBy": z.enum(["manual", "schedule", "success", "failure"]), "inputValues": z.record(z.string(), z.string()), "script": z.unknown(), "timeout": z.number().int(), "taskConfig": z.object({ "env": z.record(z.string(), z.string()).optional(), "gracefulStop": z.number().int().optional(), "logMaxSize": z.number().int().optional(), "logOnFull": z.enum(["drop_new", "drop_old", "kill_task"]).optional() }).optional(), "logUploadUrl": z.string(), "logPath": z.string() }) }),
  z.object({ "type": z.literal("execution:stop"), "v": z.literal(PROTOCOL_VERSION).optional(), "sentAt": z.string().optional(), "executionId": z.string(), "reason": z.string() }),
  z.object({ "type": z.literal("log:replayRequest"), "v": z.literal(PROTOCOL_VERSION).optional(), "sentAt": z.string().optional(), "id": z.string(), "executionId": z.string(), "fromLine": z.number().int(), "limit": z.number().int().nonnegative() }),
  z.object({ "type": z.literal("log:searchRequest"), "v": z.literal(PROTOCOL_VERSION).optional(), "sentAt": z.string().optional(), "id": z.string(), "executionId": z.string(), "query": z.string(), "regex": z.boolean().optional(), "caseSensitive": z.boolean().optional(), "limit": z.number().int().nonnegative().optional(), "fromLine": z.number().int().optional() }),
  z.object({ "type": z.literal("log:listen"), "v": z.literal(PROTOCOL_VERSION).optional(), "sentAt": z.string().optional(), "executionId": z.string() }),
  z.object({ "type": z.literal("log:stop"), "v": z.literal(PROTOCOL_VERSION).optional(), "sentAt": z.string().optional(), "executionId": z.string() }),
  z.object({ "type": z.literal("agent:restart"), "v": z.literal(PROTOCOL_VERSION).optional(), "sentAt": z.string().optional() }),
  z.object({ "type": z.literal("service:apply"), "v": z.literal(PROTOCOL_VERSION).optional(), "sentAt": z.string().optional(), "service": z.object({ "taskId": z.string(), "taskName": z.string(), "script": z.unknown(), "instances": z.number().int().min(1), "autostart": z.boolean().optional(), "restartDelay": z.number().int().optional(), "restartBackoff": z.enum(["constant", "linear", "exponential"]).optional(), "backoffResetAfter": z.number().int().optional(), "taskConfig": z.object({ "env": z.record(z.string(), z.string()).optional(), "gracefulStop": z.number().int().optional(), "logMaxSize": z.number().int().optional(), "logOnFull": z.enum(["drop_new", "drop_old", "kill_task"]).optional() }).optional() }) }),
  z.object({ "type": z.literal("service:control"), "v": z.literal(PROTOCOL_VERSION).optional(), "sentAt": z.string().optional(), "taskId": z.string(), "action": z.enum(["start", "stop", "restart"]) }),
  z.object({ "type": z.literal("error"), "v": z.literal(PROTOCOL_VERSION).optional(), "sentAt": z.string().optional(), "code": z.string(), "message": z.string(), "requestId": z.string().optional(), "executionId": z.string().optional() })
]);

export type OutboundDaemonMessage = z.infer<typeof outboundDaemonMessageSchema>;
