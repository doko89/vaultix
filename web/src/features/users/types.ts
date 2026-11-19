import type { ID } from "@/shared/types";

// User + role + identity shapes from internal/adapter/http/auth (dto.go).
// Passwords and hashes never appear in read types.

export type VaultixRole = "administrator" | "operator" | "readonly";

export interface User {
  id: ID;
  username: string;
  email: string;
  display_name: string;
  disabled: boolean;
  local_login_disabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface UserList {
  items: User[];
  total: number;
}

export interface Role {
  id: ID;
  name: string;
  description: string;
}

export interface ExternalIdentity {
  id: ID;
  user_id: ID;
  provider_id: string;
  issuer: string;
  subject: string;
  email: string;
  disabled: boolean;
  last_login_at?: string;
}

export interface CreateUserInput {
  username: string;
  email: string;
  display_name?: string;
  password?: string;
  roles?: string[];
}

export interface UpdateUserInput {
  email?: string;
  display_name?: string;
  local_login_disabled?: boolean;
}

export interface LinkIdentityInput {
  provider_id?: string;
  issuer: string;
  subject: string;
  email?: string;
}

/** Minimal session view for the admin gate (GET /auth/me, local copy). */
export interface UserSession {
  user: User;
  roles: Role[];
}

export function sessionIsAdmin(session: UserSession | undefined): boolean {
  return (session?.roles ?? []).some((r) => r.name === "administrator");
}
