import type { ID } from "@/shared/types";

// Agent shapes from internal/adapter/http/agents (dto.go + domain).
// Status is Manager-computed: online | offline | stale | revoked.

export type AgentStatus = "online" | "offline" | "stale" | "revoked";

export interface Agent {
  id: ID;
  hostname: string;
  ip_address?: string;
  os?: string;
  architecture?: string;
  version?: string;
  capabilities?: string[];
  disabled: boolean;
  status: AgentStatus;
  outdated: boolean;
  last_heartbeat_at?: string;
  disk_total_bytes?: number;
  disk_free_bytes?: number;
  enrolled_at: string;
}

export interface AgentList {
  items: Agent[];
  total: number;
}

export interface EnrollToken {
  token: string;
  credential_id: ID;
  expires_at: string;
}

export interface AgentPolicySummary {
  id: ID;
  name: string;
  enabled: boolean;
  current_version: number;
}

export interface AgentPolicies {
  agent_id: ID;
  policies: AgentPolicySummary[];
}

// Resource kinds from domain/discovery.go.
export type ResourceKind =
  | "host_path"
  | "mount"
  | "docker_container"
  | "docker_volume"
  | "docker_bind_mount"
  | "compose_project"
  | "postgres_database"
  | "mysql_database"
  | "mssql_database"
  | "redis_dataset";

export interface DiscoveredResource {
  id: ID;
  kind: string;
  name: string;
  detail?: Record<string, string>;
  discovered_at: string;
  last_seen_at: string;
}

export interface AgentResources {
  agent_id: ID;
  items: DiscoveredResource[];
}

/** DESIGN section 5 groups for the resources browser. */
export type ResourceGroup = "host" | "container" | "volume" | "database" | "stack" | "other";

const RESOURCE_GROUP_LABEL: Record<ResourceGroup, string> = {
  host: "Host filesystem",
  container: "Containers",
  volume: "Volumes & mounts",
  database: "Databases",
  stack: "Application stacks",
  other: "Other",
};

export function resourceGroupLabel(group: ResourceGroup): string {
  return RESOURCE_GROUP_LABEL[group];
}

export function groupOfKind(kind: string): ResourceGroup {
  switch (kind) {
    case "host_path":
    case "mount":
      return "host";
    case "docker_container":
      return "container";
    case "docker_volume":
    case "docker_bind_mount":
      return "volume";
    case "postgres_database":
    case "mysql_database":
    case "mssql_database":
    case "redis_dataset":
      return "database";
    case "compose_project":
      return "stack";
    default:
      return "other";
  }
}
