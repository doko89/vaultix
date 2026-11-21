// Typed fetch wrapper for the Manager REST API (TECH_SPEC section 5).
// All feature api.ts modules go through this client.
//
// Conventions handled here:
// - Same-origin cookies on every request (session auth).
// - `X-Requested-With: XMLHttpRequest` on mutating calls (CSRF rule in
//   internal/adapter/http/middleware/csrf.go).
// - 401 outside /login redirects to /login (session expired).
// - Error envelopes differ per slice; all are normalized into ApiError:
//   root/platform:  {"error": {"code": <number>, "message": ...}}
//   auth/agents/policies/jobs/restore: {"error": {"code": "<str>", "message": ...}}
//   storage: {"error": "<str>", "details": [...]}

const API_BASE = "/api";
const DEFAULT_TIMEOUT_MS = 15_000;
const CSRF_HEADER = "X-Requested-With";
const CSRF_VALUE = "XMLHttpRequest";

export class ApiError extends Error {
  readonly status: number;
  readonly code: string | undefined;
  readonly details: unknown;
  readonly requestId: string | undefined;

  constructor(
    status: number,
    message: string,
    options?: { code?: string | undefined; details?: unknown; requestId?: string | undefined },
  ) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = options?.code;
    this.details = options?.details;
    this.requestId = options?.requestId;
  }

  /** True for auth failures the UI should treat as "session gone". */
  get isAuth(): boolean {
    return this.status === 401;
  }

  /** True when the user lacks permission (server-enforced RBAC). */
  get isForbidden(): boolean {
    return this.status === 403;
  }
}

export interface RequestOptions {
  timeoutMs?: number | undefined;
  init?: Omit<RequestInit, "body" | "method"> | undefined;
  /** Skip the automatic /login redirect on 401 (used by the login form). */
  skipAuthRedirect?: boolean | undefined;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

function asString(value: unknown): string | undefined {
  return typeof value === "string" && value.length > 0 ? value : undefined;
}

/** Extract message/code/requestId from any slice's error envelope. */
function parseErrorBody(status: number, body: unknown): { message: string; code?: string; requestId?: string } {
  const fallback = `Request failed with status ${status}`;
  if (!isRecord(body)) {
    return { message: fallback };
  }
  const err = body["error"];
  // Storage slice: {"error": "<message>", "details": [...]}.
  if (typeof err === "string") {
    const details = body["details"];
    const suffix =
      Array.isArray(details) && details.length > 0
        ? `: ${details.filter((d): d is string => typeof d === "string").join("; ")}`
        : "";
    return { message: err.length > 0 ? `${err}${suffix}` : fallback };
  }
  if (isRecord(err)) {
    const message = asString(err["message"]) ?? fallback;
    const codeRaw = err["code"];
    const code = typeof codeRaw === "string" || typeof codeRaw === "number" ? String(codeRaw) : undefined;
    const requestId = asString(err["request_id"]);
    const result: { message: string; code?: string; requestId?: string } = { message };
    if (code !== undefined) result.code = code;
    if (requestId !== undefined) result.requestId = requestId;
    return result;
  }
  return { message: fallback };
}

function actionableMessage(status: number, serverMessage: string): string {
  switch (status) {
    case 400:
      return serverMessage;
    case 401:
      return "Your session has expired. Please log in again.";
    case 403:
      return serverMessage || "You do not have permission for this action.";
    case 404:
      return serverMessage || "The requested item was not found.";
    case 409:
      return serverMessage;
    case 501:
      return "This operation is not available yet on the Manager.";
    default:
      if (status >= 500) {
        return "The Manager hit an internal error. Please try again later.";
      }
      return serverMessage;
  }
}

function maybeRedirectToLogin(status: number, skip: boolean): void {
  if (status !== 401 || skip) return;
  if (typeof window === "undefined") return;
  const path = window.location.pathname;
  if (path.startsWith("/login")) return;
  const next = `${path}${window.location.search}`;
  window.location.href = `/login?next=${encodeURIComponent(next)}`;
}

const MUTATING = new Set(["POST", "PUT", "PATCH", "DELETE"]);

async function request<T>(
  path: string,
  method: string,
  body?: unknown,
  options?: RequestOptions,
): Promise<T> {
  const timeoutMs = options?.timeoutMs ?? DEFAULT_TIMEOUT_MS;
  const headers: Record<string, string> = {};
  const extra = options?.init?.headers;
  if (isRecord(extra)) {
    for (const [k, v] of Object.entries(extra)) {
      if (typeof v === "string") headers[k] = v;
    }
  } else if (extra instanceof Headers) {
    extra.forEach((v, k) => {
      headers[k] = v;
    });
  }
  const isForm = body instanceof FormData;
  if (body !== undefined && !isForm) {
    headers["Content-Type"] = "application/json";
  }
  if (MUTATING.has(method)) {
    headers[CSRF_HEADER] = CSRF_VALUE;
  }
  const init: RequestInit = {
    ...options?.init,
    method,
    headers,
    credentials: "include",
    signal: AbortSignal.timeout(timeoutMs),
  };
  if (body !== undefined) {
    init.body = isForm ? body : JSON.stringify(body);
  }

  let response: Response;
  try {
    response = await fetch(`${API_BASE}${path}`, init);
  } catch (cause) {
    if (cause instanceof DOMException && cause.name === "TimeoutError") {
      throw new ApiError(0, "The Manager did not respond in time. Please try again.", {
        code: "timeout",
      });
    }
    throw new ApiError(0, "Cannot reach the Manager. Check your connection and try again.", {
      code: "network",
    });
  }

  if (response.status === 204) {
    return undefined as T;
  }

  const payload: unknown = await response.json().catch(() => null);

  if (!response.ok) {
    const parsed = parseErrorBody(response.status, payload);
    maybeRedirectToLogin(response.status, options?.skipAuthRedirect === true);
    throw new ApiError(response.status, actionableMessage(response.status, parsed.message), {
      code: parsed.code,
      details: payload,
      requestId: parsed.requestId,
    });
  }

  return payload as T;
}

/** Restore/catalog slice wraps success payloads in {"data": ...}. */
interface DataEnvelope<T> {
  data: T;
}

async function requestData<T>(
  path: string,
  method: string,
  body?: unknown,
  options?: RequestOptions,
): Promise<T> {
  const payload = await request<DataEnvelope<T>>(path, method, body, options);
  if (!isRecord(payload) || !("data" in payload)) {
    throw new ApiError(502, "The Manager returned an unexpected response shape.", {
      code: "bad_envelope",
      details: payload,
    });
  }
  return (payload as DataEnvelope<T>).data;
}

export const api = {
  get<T>(path: string, options?: RequestOptions): Promise<T> {
    return request<T>(path, "GET", undefined, options);
  },
  post<T>(path: string, body?: unknown, options?: RequestOptions): Promise<T> {
    return request<T>(path, "POST", body, options);
  },
  put<T>(path: string, body?: unknown, options?: RequestOptions): Promise<T> {
    return request<T>(path, "PUT", body, options);
  },
  patch<T>(path: string, body?: unknown, options?: RequestOptions): Promise<T> {
    return request<T>(path, "PATCH", body, options);
  },
  delete<T>(path: string, options?: RequestOptions): Promise<T> {
    return request<T>(path, "DELETE", undefined, options);
  },
  /** GET unwrapping the restore slice's {"data": ...} envelope. */
  getData<T>(path: string, options?: RequestOptions): Promise<T> {
    return requestData<T>(path, "GET", undefined, options);
  },
  /** POST unwrapping the restore slice's {"data": ...} envelope. */
  postData<T>(path: string, body?: unknown, options?: RequestOptions): Promise<T> {
    return requestData<T>(path, "POST", body, options);
  },
};

/** Render an ApiError (or unknown failure) as a one-line message. */
export function errorText(err: unknown): string {
  if (err instanceof ApiError) return err.message;
  if (err instanceof Error) return err.message;
  return "Something went wrong. Please try again.";
}

/** Build a query string from defined params only. */
export function qs(params: Record<string, string | number | boolean | undefined>): string {
  const search = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== "") search.set(k, String(v));
  }
  const s = search.toString();
  return s.length > 0 ? `?${s}` : "";
}
