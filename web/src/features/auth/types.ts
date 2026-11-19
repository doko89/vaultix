import type { ID } from "@/shared/types";

// Session + user shapes from internal/adapter/http/auth (dto.go).
// Passwords, hashes, tokens, and client secrets never appear here.

export interface SessionUser {
  id: ID;
  username: string;
  email: string;
  display_name: string;
  disabled: boolean;
  local_login_disabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface SessionRole {
  id: ID;
  name: string;
  description: string;
}

export interface Session {
  user: SessionUser;
  roles: SessionRole[];
  expires_at: string;
}

export interface PublicProvider {
  id: ID;
  name: string;
}

export function roleNames(session: Session | undefined): string[] {
  if (session === undefined) return [];
  return session.roles.map((r) => r.name);
}

export function isAdmin(session: Session | undefined): boolean {
  return roleNames(session).includes("administrator");
}

/** Display name with sensible fallbacks. */
export function displayName(user: SessionUser): string {
  if (user.display_name !== "") return user.display_name;
  if (user.username !== "") return user.username;
  return user.email;
}
