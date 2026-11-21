import { api, qs } from "@/shared/lib/api";
import type { ID } from "@/shared/types";
import type {
  Agent,
  AgentArtifactList,
  AgentList,
  AgentPolicies,
  AgentResources,
  AgentUpdateInfo,
  AgentUpdateSlot,
  EnrollToken,
  UpdateCheckResult,
} from "./types";

export interface AgentFilters {
  status?: string | undefined;
  q?: string | undefined;
  limit?: number | undefined;
  offset?: number | undefined;
}

export function listAgents(filters: AgentFilters = {}): Promise<AgentList> {
  return api.get<AgentList>(
    `/agents${qs({ status: filters.status, q: filters.q, limit: filters.limit, offset: filters.offset })}`,
  );
}

export function getAgent(agentId: ID): Promise<Agent> {
  return api.get<Agent>(`/agents/${encodeURIComponent(agentId)}`);
}

export function issueEnrollToken(ttlHours?: number): Promise<EnrollToken> {
  return api.post<EnrollToken>("/agents/enroll-tokens", ttlHours === undefined ? {} : { ttl_hours: ttlHours });
}

export function setAgentDisabled(agentId: ID, disabled: boolean): Promise<Agent> {
  return api.patch<Agent>(`/agents/${encodeURIComponent(agentId)}`, { disabled });
}

export function revokeAgent(agentId: ID): Promise<Agent> {
  return api.post<Agent>(`/agents/${encodeURIComponent(agentId)}/revoke`);
}

export function deleteAgent(agentId: ID): Promise<void> {
  return api.delete<void>(`/agents/${encodeURIComponent(agentId)}`);
}

export function listAgentPolicies(agentId: ID): Promise<AgentPolicies> {
  return api.get<AgentPolicies>(`/agents/${encodeURIComponent(agentId)}/policies`);
}

export function listAgentResources(agentId: ID, kind?: string): Promise<AgentResources> {
  return api.get<AgentResources>(
    `/agents/${encodeURIComponent(agentId)}/resources${qs({ kind })}`,
  );
}

export function listArtifacts(): Promise<AgentArtifactList> {
  return api.get<AgentArtifactList>("/agents/updates/artifacts");
}

export function checkUpdates(): Promise<UpdateCheckResult> {
  return api.post<UpdateCheckResult>("/agents/updates/check", undefined, { timeoutMs: 300_000 });
}

export function deleteArtifact(artifactId: ID): Promise<void> {
  return api.delete<void>(`/agents/updates/artifacts/${encodeURIComponent(artifactId)}`);
}

export function describeUpdate(agentId: ID): Promise<AgentUpdateInfo> {
  return api.get<AgentUpdateInfo>(`/agents/${encodeURIComponent(agentId)}/update`);
}

export function requestUpdate(agentId: ID, artifactId: ID): Promise<AgentUpdateSlot> {
  return api.post<AgentUpdateSlot>(`/agents/${encodeURIComponent(agentId)}/update`, { artifact_id: artifactId });
}
