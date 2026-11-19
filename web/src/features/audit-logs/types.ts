import type { ID } from "@/shared/types";

// Audit shapes from internal/adapter/http/platform (dto.go). Read-only:
// the API exposes no secrets or backup contents here.

export interface AuditEntry {
  id: ID;
  actor_id?: string;
  actor_type: string;
  action: string;
  target_type?: string;
  target_id?: string;
  outcome: string;
  detail?: string;
  ip_address?: string;
  created_at: string;
}

export interface AuditList {
  items: AuditEntry[];
  total: number;
}

export interface AuditFilters {
  actor?: string | undefined;
  action?: string | undefined;
  target_type?: string | undefined;
  target_id?: string | undefined;
  outcome?: string | undefined;
  since?: string | undefined;
  until?: string | undefined;
  limit?: number | undefined;
  offset?: number | undefined;
}

export const AUDIT_ACTIONS = [
  "login",
  "login_failed",
  "logout",
  "agent_enroll",
  "agent_revoke",
  "policy_change",
  "job_start",
  "job_cancel",
  "job_retry",
  "restore_approve",
  "restore_start",
  "storage_change",
  "secret_change",
  "retention_run",
  "user_change",
  "role_change",
  "oidc_change",
  "oidc_link_change",
  "notification_send",
] as const;
