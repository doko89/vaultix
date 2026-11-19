import type { AgentPresence, ID } from "@/shared/types";

export interface Agent {
  id: ID;
  hostname: string;
  ip: string;
  os: string;
  arch: string;
  version: string;
  presence: AgentPresence;
  lastHeartbeat: string;
  diskTotalBytes: number;
  diskFreeBytes: number;
}

export type ResourceKind = "host-path" | "container" | "volume" | "database" | "stack";

export interface DiscoveredResource {
  id: ID;
  agentId: ID;
  kind: ResourceKind;
  name: string;
}
