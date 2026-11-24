import {
  Catch,
  HttpException,
  type ArgumentsHost,
  type ExceptionFilter,
  type LoggerService,
} from "@nestjs/common";
import type { FastifyReply, FastifyRequest } from "fastify";

import { isUnavailable } from "../adapters/postgres.js";
import {
  InvalidParameterError,
  NotFoundError,
  UnavailableError,
  type ParameterIssue,
} from "../domain/errors.js";

/** RFC 9457 Problem Details, sama dengan schema Problem di kontrak. */
export interface Problem {
  type: string;
  title: string;
  status: number;
  detail?: string;
  instance?: string;
  errors?: ParameterIssue[];
}

export const PROBLEM_TYPES = {
  invalid: "/problems/invalid-parameter",
  notFound: "/problems/not-found",
  unavailable: "/problems/unavailable",
  internal: "/problems/internal",
  http: "about:blank",
} as const;

/** Memetakan galat apa pun menjadi Problem. Galat tak dikenal tidak membocorkan pesannya. */
export function toProblem(err: unknown, instance: string): Problem {
  if (err instanceof InvalidParameterError) {
    return {
      type: PROBLEM_TYPES.invalid,
      title: "Parameter tidak valid",
      status: 400,
      detail: err.message,
      instance,
      errors: [...err.issues],
    };
  }
  if (err instanceof NotFoundError) {
    return {
      type: PROBLEM_TYPES.notFound,
      title: "Tidak ditemukan",
      status: 404,
      detail: err.message,
      instance,
    };
  }
  if (err instanceof HttpException) {
    const status = err.getStatus();
    return { type: PROBLEM_TYPES.http, title: err.message, status, instance };
  }
  if (err instanceof UnavailableError || isUnavailable(err)) {
    return {
      type: PROBLEM_TYPES.unavailable,
      title: "Layanan sementara tidak tersedia",
      status: 503,
      detail: "Database tidak bisa dijangkau atau terlalu lambat; coba lagi sebentar.",
      instance,
    };
  }
  return { type: PROBLEM_TYPES.internal, title: "Galat internal", status: 500, instance };
}

/** Filter global: semua galat keluar sebagai application/problem+json. */
@Catch()
export class ProblemFilter implements ExceptionFilter {
  constructor(private readonly logger: LoggerService) {}

  catch(err: unknown, host: ArgumentsHost): void {
    const http = host.switchToHttp();
    const req = http.getRequest<FastifyRequest>();
    const reply = http.getResponse<FastifyReply>();
    const problem = toProblem(err, req.url);
    if (problem.status >= 500) {
      this.logger.error(
        `${req.method} ${req.url} -> ${String(problem.status)}: ${err instanceof Error ? (err.stack ?? err.message) : String(err)}`,
        "ProblemFilter",
      );
    }
    void reply
      .status(problem.status)
      .header("content-type", "application/problem+json")
      .header("cache-control", "no-store")
      .headers(problem.status === 503 ? { "retry-after": "5" } : {})
      .send(problem);
  }
}
