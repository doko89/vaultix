import type { ID } from "@/shared/types";

// Settings shapes from internal/adapter/http/platform (dto.go).
// Client secrets are write-only: reads return has_client_secret only.

export interface GlobalSettings {
  local_login_enabled: boolean;
  default_role: string;
  session_lifetime_minutes: number;
  session_idle_minutes: number;
  heartbeat_timeout_seconds: number;
  global_concurrency: number;
}

export function validateSettings(s: GlobalSettings): string[] {
  const errors: string[] = [];
  if (!["", "administrator", "operator", "readonly"].includes(s.default_role)) {
    errors.push("Default role must be administrator, operator, readonly, or empty (deny).");
  }
  if (s.session_lifetime_minutes < 0 || s.session_idle_minutes < 0) {
    errors.push("Session timeouts must be non-negative.");
  }
  if (s.session_lifetime_minutes > 0 && s.session_idle_minutes > s.session_lifetime_minutes) {
    errors.push("Idle timeout must not exceed session lifetime.");
  }
  if (s.heartbeat_timeout_seconds < 0 || s.global_concurrency < 0) {
    errors.push("Heartbeat timeout and concurrency must be non-negative.");
  }
  return errors;
}

export interface OidcProvider {
  id: ID;
  name: string;
  issuer: string;
  enabled: boolean;
  discovery_url?: string;
  auth_url?: string;
  token_url?: string;
  jwks_url?: string;
  userinfo_url?: string;
  end_session?: string;
  client_id: string;
  has_client_secret: boolean;
  scopes?: string[];
  group_claim_names?: string[];
  group_role_mapping?: Record<string, string>;
  default_role?: string;
  jit_provisioning: boolean;
  created_at?: string;
  updated_at?: string;
}

export interface OidcProviderInput {
  name: string;
  issuer: string;
  enabled: boolean;
  discovery_url?: string;
  auth_url?: string;
  token_url?: string;
  jwks_url?: string;
  userinfo_url?: string;
  end_session?: string;
  client_id: string;
  client_secret?: string;
  scopes?: string[];
  group_claim_names?: string[];
  group_role_mapping?: Record<string, string>;
  default_role?: string;
  jit_provisioning?: boolean;
}

export function validateProviderInput(input: OidcProviderInput): string[] {
  const errors: string[] = [];
  if (input.name.trim() === "") errors.push("Provider name is required.");
  if (input.issuer.trim() === "") errors.push("Issuer is required.");
  if (input.client_id.trim() === "") errors.push("Client ID is required.");
  return errors;
}
