// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

export interface LoggerOptions {
  enabled: boolean;
  prefix: string;
}

function formatMessage(
  prefix: string,
  context: string,
  message: string,
): string {
  const timestamp = new Date().toISOString();
  return `${prefix} ${timestamp} [${context}] ${message}`;
}

export class Logger {
  constructor(
    private readonly options: LoggerOptions,
    private readonly context: string,
  ) {}

  debug(message: string, ...args: unknown[]): void {
    if (!this.options.enabled) return;
    console.debug(formatMessage(this.options.prefix, this.context, message), ...args);
  }

  info(message: string, ...args: unknown[]): void {
    if (!this.options.enabled) return;
    console.info(formatMessage(this.options.prefix, this.context, message), ...args);
  }

  warn(message: string, ...args: unknown[]): void {
    if (!this.options.enabled) return;
    console.warn(formatMessage(this.options.prefix, this.context, message), ...args);
  }

  error(message: string, error?: unknown, ...args: unknown[]): void {
    console.error(formatMessage(this.options.prefix, this.context, message), error, ...args);
  }
}

export function createLoggerFactory(options: Partial<LoggerOptions> = {}) {
  const resolved: LoggerOptions = {
    enabled: true,
    prefix: "[runwisp]",
    ...options,
  };
  return (context: string) => new Logger(resolved, context);
}
