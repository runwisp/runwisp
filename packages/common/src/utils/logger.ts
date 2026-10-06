// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

function formatMessage(context: string, message: string): string {
  return `[runwisp] ${new Date().toISOString()} [${context}] ${message}`;
}

export class Logger {
  constructor(private readonly context: string) {}

  debug(message: string, ...args: unknown[]): void {
    console.debug(formatMessage(this.context, message), ...args);
  }

  info(message: string, ...args: unknown[]): void {
    console.info(formatMessage(this.context, message), ...args);
  }

  warn(message: string, ...args: unknown[]): void {
    console.warn(formatMessage(this.context, message), ...args);
  }

  error(message: string, error?: unknown, ...args: unknown[]): void {
    console.error(formatMessage(this.context, message), error, ...args);
  }
}

export function createLogger(context: string): Logger {
  return new Logger(context);
}
