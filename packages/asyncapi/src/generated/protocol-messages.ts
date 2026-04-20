// Generated from asyncapi.yaml
import { z } from "zod";

export const PROTOCOL_VERSION = 1;

export const inboundDaemonMessageSchema = z.discriminatedUnion("type", [
  z.object({ "type": z.literal("ping"), "v": z.literal(PROTOCOL_VERSION).optional(), "sentAt": z.string().optional(), "id": z.string().optional() }),
  z.object({ "type": z.literal("execution:update"), "v": z.literal(PROTOCOL_VERSION).optional(), "sentAt": z.string().optional(), "executionId": z.string(), "status": z.enum(["running", "ok", "err", "stopped", "timeout"]), "exitCode": z.number().int().nullable().optional(), "startedAt": z.coerce.date().nullable().optional(), "finishedAt": z.coerce.date().nullable().optional() }),
  z.object({ "type": z.literal("log:chunk"), "v": z.literal(PROTOCOL_VERSION).optional(), "sentAt": z.string().optional(), "executionId": z.string(), "data": z.string(), "offset": z.number().int().nonnegative(), "final": z.boolean() }),
  z.object({ "type": z.literal("log:response"), "v": z.literal(PROTOCOL_VERSION).optional(), "sentAt": z.string().optional(), "id": z.string(), "executionId": z.string(), "data": z.string(), "offset": z.number().int().nonnegative(), "final": z.boolean() }),
  z.object({ "type": z.literal("error"), "v": z.literal(PROTOCOL_VERSION).optional(), "sentAt": z.string().optional(), "code": z.string(), "message": z.string(), "requestId": z.string().optional() })
]);

export type InboundDaemonMessage = z.infer<typeof inboundDaemonMessageSchema>;

export const outboundDaemonMessageSchema = z.discriminatedUnion("type", [
  z.object({ "type": z.literal("auth:result"), "v": z.literal(PROTOCOL_VERSION).optional(), "sentAt": z.string().optional(), "success": z.boolean(), "connectionId": z.string().optional(), "error": z.string().optional() }),
  z.object({ "type": z.literal("pong"), "v": z.literal(PROTOCOL_VERSION).optional(), "sentAt": z.string().optional() }),
  z.object({ "type": z.literal("execution:dispatch"), "v": z.literal(PROTOCOL_VERSION).optional(), "sentAt": z.string().optional(), "execution": z.object({ "id": z.string(), "taskId": z.string(), "taskName": z.string(), "executionId": z.string(), "priority": z.number().int(), "triggeredBy": z.enum(["manual", "schedule", "success", "failure"]), "inputValues": z.record(z.string(), z.string()), "script": z.unknown(), "timeout": z.number().int() }) }),
  z.object({ "type": z.literal("execution:stop"), "v": z.literal(PROTOCOL_VERSION).optional(), "sentAt": z.string().optional(), "executionId": z.string(), "reason": z.string() }),
  z.object({ "type": z.literal("log:request"), "v": z.literal(PROTOCOL_VERSION).optional(), "sentAt": z.string().optional(), "id": z.string(), "executionId": z.string(), "offset": z.number().int().nonnegative(), "limit": z.number().int().nonnegative() }),
  z.object({ "type": z.literal("log:listen"), "v": z.literal(PROTOCOL_VERSION).optional(), "sentAt": z.string().optional(), "executionId": z.string() }),
  z.object({ "type": z.literal("log:stop"), "v": z.literal(PROTOCOL_VERSION).optional(), "sentAt": z.string().optional(), "executionId": z.string() }),
  z.object({ "type": z.literal("error"), "v": z.literal(PROTOCOL_VERSION).optional(), "sentAt": z.string().optional(), "code": z.string(), "message": z.string(), "requestId": z.string().optional() })
]);

export type OutboundDaemonMessage = z.infer<typeof outboundDaemonMessageSchema>;
