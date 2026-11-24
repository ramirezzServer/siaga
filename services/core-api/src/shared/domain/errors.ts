/** Satu parameter permintaan yang tidak valid. */
export interface ParameterIssue {
  readonly parameter: string;
  readonly message: string;
}

/** Permintaan tidak valid (HTTP 400). Dilempar domain dan use case, dipetakan di lapisan HTTP. */
export class InvalidParameterError extends Error {
  readonly issues: readonly ParameterIssue[];

  constructor(issues: readonly ParameterIssue[]) {
    super(issues.map((i) => `${i.parameter}: ${i.message}`).join("; "));
    this.name = "InvalidParameterError";
    this.issues = issues;
  }

  static of(parameter: string, message: string): InvalidParameterError {
    return new InvalidParameterError([{ parameter, message }]);
  }
}

/** Sumber daya tidak ditemukan (HTTP 404). */
export class NotFoundError extends Error {
  constructor(what: string) {
    super(`${what} tidak ditemukan`);
    this.name = "NotFoundError";
  }
}

/** Dependensi (database) sedang tidak bisa dipakai (HTTP 503). */
export class UnavailableError extends Error {
  constructor(what: string, options?: ErrorOptions) {
    super(`${what} tidak tersedia`, options);
    this.name = "UnavailableError";
  }
}
