import type { ID, JobState } from "@/shared/types";

// Job shapes from internal/adapter/http/jobs (jobs.go DTOs).
// Pointer DTO fields without `omitempty` serialize as null (not absent)
// when unmeasurable, so the matching fields below accept null.

export interface Job {
  id: ID;
  agent_id: ID;
  policy_id: ID;
  policy_version: number;
  schedule_id?: string;
  trigger: string;
  state: JobState;
  progress?: number | null;
  bytes_read?: number | null;
  bytes_compressed?: number | null;
  bytes_uploaded?: number | null;
  compression_ratio?: number | null;
  encrypted: boolean;
  key_ref?: string;
  storage_id?: string;
  snapshot_id?: string;
  error_code?: string;
  error_message?: string;
  retry_of?: string;
  attempt: number;
  started_at?: string | null;
  finished_at?: string | null;
  created_by?: string;
  created_at: string;
  updated_at: string;
}

export interface JobAttempt {
  id: ID;
  attempt: number;
  state: string;
}

export interface JobDetail extends Job {
  retry_history: JobAttempt[];
}

export interface JobList {
  items: Job[];
  total: number;
  limit: number;
  offset: number;
}

export interface JobFilters {
  agent_id?: string | undefined;
  policy_id?: string | undefined;
  state?: string | undefined;
  since?: string | undefined;
  until?: string | undefined;
  limit?: number | undefined;
  offset?: number | undefined;
}

/** States that keep changing and deserve polling. */
const ACTIVE: ReadonlySet<string> = new Set(["queued", "assigned", "running", "uploading", "verifying"]);

export function isActiveState(state: string): boolean {
  return ACTIVE.has(state);
}

export function encryptionLabel(job: Pick<Job, "encrypted" | "key_ref">): string {
  if (!job.encrypted) return "Unencrypted";
  return job.key_ref !== undefined && job.key_ref !== "" ? `Encrypted · key ${job.key_ref}` : "Encrypted";
}
