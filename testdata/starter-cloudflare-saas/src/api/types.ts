import type { SessionUser } from "../lib/auth";

export type AppEnv = {
  Bindings: Env;
  Variables: { user: SessionUser };
};

/** Error with an HTTP status; rendered as { error: { code, message } } by the app's onError. */
export class HttpError extends Error {
  constructor(
    readonly status: 400 | 401 | 403 | 404 | 413 | 415 | 429,
    readonly code: string,
    message: string,
    readonly headers: Record<string, string> = {},
  ) {
    super(message);
  }
}
