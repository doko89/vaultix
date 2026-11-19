import type { ID, JobState } from "@/shared/types";

// Dashboard shapes from internal/adapter/http/platform (dto.go).

export interface AgentCounts {
  total: number;
  online: number;
  offline: number;
  stale: number;
  revoked: number;
}

export interface JobSummary {
  id: ID;
  agent_id: ID;
  policy_id: ID;
  state: JobState;
  started_at?: string;
  finished_at?: string;
  error?: string;
}

export interface LastBackup {
  agent_id?: string;
  agent_name?: string;
  policy_id?: string;
  policy_name?: string;
  job_id?: string;
  state?: JobState;
  finished_at?: string;
  has_succeeded: boolean;
}

export interface PolicyGap {
  policy_id: ID;
  policy_name?: string;
  last_success_at?: string;
}

export interface DayPoint {
  date: string;
  succeeded: number;
  failed: number;
  bytes_uploaded: number;
}

export interface StorageUsage {
  bytes_used: number;
  bytes_total?: number;
  object_count?: number;
  snapshot_count?: number;
  available: boolean;
}

export interface Dashboard {
  agents: AgentCounts;
  jobs_by_status: Record<string, number>;
  recent_jobs: JobSummary[];
  last_backup_per_agent: LastBackup[];
  last_backup_per_policy: LastBackup[];
  policies_without_recent_success: PolicyGap[];
  trend: DayPoint[];
  storage: StorageUsage;
}

// Back-compat alias for the scaffold type name.
export type DashboardSummary = Dashboard;
