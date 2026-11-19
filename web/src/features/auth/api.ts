import { api } from "@/shared/lib/api";
import type { PublicProvider, Session } from "./types";

export function login(identifier: string, password: string): Promise<Session> {
  // skipAuthRedirect: a bad login is a form error, not a session expiry.
  return api.post<Session>("/auth/login", { identifier, password }, { skipAuthRedirect: true });
}

export interface LogoutResult {
  oidc_logout_url?: string;
}

export function logout(): Promise<LogoutResult> {
  return api.post<LogoutResult>("/auth/logout");
}

export function me(): Promise<Session> {
  return api.get<Session>("/auth/me");
}

export function listPublicProviders(): Promise<{ items: PublicProvider[] }> {
  return api.get<{ items: PublicProvider[] }>("/auth/providers/public");
}

/** OIDC start is a redirect endpoint: navigate the browser, don't fetch. */
export function oidcStartUrl(providerId: string): string {
  return `/api/auth/oidc/start?provider=${encodeURIComponent(providerId)}`;
}
