import type { ID, JobState } from "@/shared/types";

// Restore shapes from internal/adapter/http/restore (dto.go). Success
// payloads arrive wrapped in {"data": ...} (handled in api.ts).

export type RestoreKind = "file" | "database" | "volume" | "stack";

export const RESTORE_KINDS: RestoreKind[] = ["file", "database", "volume", "stack"];

/** Minimal snapshot view for the wizard picker (GET /api/catalog). */
export interface RestoreSnapshot {
  id: ID;
  agent_id: ID;
  policy_id: ID;
  target_type: string;
  selector: string;
  status: string;
  encryption: string;
  key_ref?: string;
  restorable: boolean;
  completed_at?: string;
  created_at: string;
}

export interface RestoreCatalogPage {
  items: RestoreSnapshot[];
  total: number;
  limit: number;
  offset: number;
}

export interface PlanRequest {
  kind: RestoreKind;
  snapshot_id?: string | undefined;
  snapshot_ids?: string[] | undefined;
  engine?: string | undefined;
  paths?: string[] | undefined;
  destination?: string | undefined;
  target_db?: string | undefined;
  order?: string[] | undefined;
  target_agent_id?: string | undefined;
  requested_by: string;
}

export interface PlanStep {
  sequence: number;
  kind: string;
  snapshot_id: ID;
  source: string;
  destination: string;
  notes?: string[];
}

export interface RestorePlan {
  id: ID;
  kind: string;
  agent_id: ID;
  snapshot_ids: ID[];
  steps: PlanStep[];
  warnings: string[];
  compatibility?: string[];
  alternate_destination: boolean;
  paths?: string[];
  requested_by: string;
  created_at: number;
  expires_at: number;
}

export interface Confirmation {
  token: string;
  plan_id: ID;
  expires_at: string;
}

export interface RestoreJob {
  id: ID;
  agent_id: ID;
  snapshot_id: ID;
  target_path?: string;
  paths?: string[];
  state: JobState;
  progress?: number;
  error_code?: string;
  error_message?: string;
  approved_by?: string;
  created_at: string;
  updated_at: string;
}

const ACTIVE: ReadonlySet<string> = new Set(["queued", "assigned", "running", "uploading", "verifying"]);

export function isActiveRestoreState(state: string): boolean {
  return ACTIVE.has(state);
}

/** Map a catalog target type to the restore kind the planner expects. */
export function kindForTargetType(targetType: string): RestoreKind {
  switch (targetType) {
    case "docker_volume":
      return "volume";
    case "postgres":
    case "mysql":
    case "mssql":
    case "redis":
      return "database";
    case "app_stack":
      return "stack";
    case "host_filesystem":
    case "docker_config":
    default:
      return "file";
  }
}

/** Minimal session view for requested_by/actor attribution (GET /auth/me). */
export interface CurrentActor {
  user: { id: ID; username: string; display_name: string; email: string };
}
