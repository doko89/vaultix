import { api } from "@/shared/lib/api";
import type { ID } from "@/shared/types";
import type { Agent, DiscoveredResource } from "./types";

export function listAgents(): Promise<Agent[]> {
  return api.get<Agent[]>("/agents");
}

export function listAgentResources(agentId: ID): Promise<DiscoveredResource[]> {
  return api.get<DiscoveredResource[]>(`/agents/${agentId}/resources`);
}
