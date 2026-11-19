// Typed fetch wrapper for the Manager REST API (TECH_SPEC section 5).
// All feature api.ts modules go through this client; swap the internals
// for a generated client if oapi-codegen is adopted.

const API_BASE = "/api";
const DEFAULT_TIMEOUT_MS = 15_000;

export class ApiError extends Error {
  readonly status: number;
  readonly details: unknown;

  constructor(status: number, message: string, details?: unknown) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.details = details;
  }
}

export interface RequestOptions {
  timeoutMs?: number;
  init?: Omit<RequestInit, "body" | "method">;
}

interface ErrorBody {
  message?: unknown;
}

function errorMessage(status: number, body: unknown): string {
  if (typeof body === "object" && body !== null && "message" in body) {
    const message = (body as ErrorBody).message;
    if (typeof message === "string" && message.length > 0) {
      return message;
    }
  }
  return `Request failed with status ${status}`;
}

async function request<T>(
  path: string,
  method: string,
  body?: unknown,
  options?: RequestOptions,
): Promise<T> {
  const timeoutMs = options?.timeoutMs ?? DEFAULT_TIMEOUT_MS;
  const init: RequestInit = {
    ...options?.init,
    method,
    headers: {
      "Content-Type": "application/json",
      ...options?.init?.headers,
    },
    signal: AbortSignal.timeout(timeoutMs),
  };
  if (body !== undefined) {
    init.body = JSON.stringify(body);
  }
  const response = await fetch(`${API_BASE}${path}`, init);

  if (response.status === 204) {
    return undefined as T;
  }

  const payload: unknown = await response.json().catch(() => null);

  if (!response.ok) {
    throw new ApiError(response.status, errorMessage(response.status, payload), payload);
  }

  return payload as T;
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
};
