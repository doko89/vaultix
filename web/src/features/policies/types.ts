import type { ID } from "@/shared/types";

// Policy + schedule shapes from internal/adapter/http/policies (dto.go).
// Target-type rules mirror internal/usecase/policy/validate.go.

export type TargetType =
  | "host_filesystem"
  | "docker_config"
  | "docker_volume"
  | "postgres"
  | "mysql"
  | "mssql"
  | "redis"
  | "app_stack";

export const TARGET_TYPES: TargetType[] = [
  "host_filesystem",
  "docker_config",
  "docker_volume",
  "postgres",
  "mysql",
  "mssql",
  "redis",
  "app_stack",
];

export const TARGET_TYPE_LABEL: Record<TargetType, string> = {
  host_filesystem: "Host filesystem",
  docker_config: "Docker config",
  docker_volume: "Docker volume",
  postgres: "PostgreSQL",
  mysql: "MySQL / MariaDB",
  mssql: "SQL Server",
  redis: "Redis",
  app_stack: "Application stack",
};

/** Fixed per-type backup method (server default; sent explicitly). */
export const TARGET_METHOD: Record<TargetType, string> = {
  host_filesystem: "full",
  docker_config: "full",
  docker_volume: "full",
  postgres: "logical_dump",
  mysql: "logical_dump",
  mssql: "native_full",
  redis: "rdb_snapshot",
  app_stack: "full",
};

export interface Compression {
  algorithm: string;
  level: number;
}

export interface Encryption {
  enabled: boolean;
  key_ref: string;
}

export interface Retention {
  keep_last_n: number;
  keep_days: number;
  keep_minimum: number;
}

export interface Policy {
  id: ID;
  name: string;
  description: string;
  agent_ids: ID[];
  storage_id: string;
  compression: Compression;
  encryption: Encryption;
  enabled: boolean;
  current_version: number;
  created_by: string;
  created_at: string;
  updated_at: string;
}

export interface PolicyList {
  items: Policy[];
  total: number;
}

export interface PolicyVersionEnvelope {
  policy: Policy;
  version: PolicyVersion;
}

export interface PolicyTarget {
  id: ID;
  type: string;
  method: string;
  selector: string;
  include?: string[];
  exclude?: string[];
  options?: Record<string, string>;
  credential_ref: string;
}

export interface TargetList {
  items: PolicyTarget[];
  total: number;
}

export interface PolicyVersion {
  version: number;
  policy_id: ID;
  created_by: string;
  created_at: string;
  targets: PolicyTarget[];
  schedule?: Schedule;
  retention: Retention;
  compression: Compression;
  encryption: Encryption;
  storage_id: string;
  note: string;
}

export interface VersionList {
  items: PolicyVersion[];
  total: number;
}

export interface TargetInput {
  type: TargetType;
  method?: string | undefined;
  selector: string;
  include?: string[] | undefined;
  exclude?: string[] | undefined;
  options?: Record<string, string> | undefined;
  credential_ref?: string | undefined;
}

export interface PolicyInput {
  name: string;
  description?: string | undefined;
  agent_ids: ID[];
  storage_id?: string | undefined;
  compression: Compression;
  encryption: Encryption;
  retention: Retention;
  targets: TargetInput[];
  note?: string | undefined;
}

// --- Schedules (owned by the policies feature, TECH_SPEC 1.2) ---

export type ScheduleKind = "one_time" | "hourly" | "daily" | "weekly" | "cron";

export const SCHEDULE_KINDS: ScheduleKind[] = ["one_time", "hourly", "daily", "weekly", "cron"];

export const SCHEDULE_KIND_LABEL: Record<ScheduleKind, string> = {
  one_time: "One-time",
  hourly: "Hourly",
  daily: "Daily",
  weekly: "Weekly",
  cron: "Cron",
};

export interface Schedule {
  id: ID;
  policy_id: ID;
  kind: string;
  time_of_day?: string;
  weekday?: number;
  interval_hours?: number;
  cron_expr?: string;
  run_at?: string;
  timezone: string;
  paused: boolean;
  max_concurrency: number;
  next_run_at?: string;
  created_at: string;
  updated_at: string;
}

export interface ScheduleList {
  items: Schedule[];
  total: number;
}

export interface ScheduleInput {
  policy_id: string;
  kind: ScheduleKind;
  time_of_day?: string | undefined;
  weekday?: number | undefined;
  interval_hours?: number | undefined;
  cron_expr?: string | undefined;
  run_at?: string | undefined;
  timezone: string;
  max_concurrency?: number | undefined;
}

export interface ManualRunJob {
  id: ID;
  agent_id: ID;
  policy_id: ID;
  policy_version: number;
  schedule_id: ID;
  trigger: string;
  state: string;
  created_by: string;
  created_at: string;
}

// --- Reference lists (duplicated here: features import only shared/) ---

export interface AgentOption {
  id: ID;
  hostname: string;
}

export interface StorageOption {
  id: ID;
  name: string;
}

export interface CredentialOption {
  id: ID;
  name: string;
  kind: string;
  key_ref: string;
}
