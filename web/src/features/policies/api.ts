import { api, qs } from "@/shared/lib/api";
import type { ID } from "@/shared/types";
import type {
  AgentOption,
  CredentialOption,
  ManualRunJob,
  Policy,
  PolicyInput,
  PolicyList,
  PolicyTarget,
  PolicyVersion,
  PolicyVersionEnvelope,
  Schedule,
  ScheduleInput,
  ScheduleList,
  StorageOption,
  TargetInput,
  TargetList,
  VersionList,
} from "./types";

export function listPolicies(
  opts: { q?: string | undefined; limit?: number | undefined; offset?: number | undefined } = {},
): Promise<PolicyList> {
  return api.get<PolicyList>(`/policies${qs({ q: opts.q, limit: opts.limit, offset: opts.offset })}`);
}

export function getPolicy(policyId: ID): Promise<Policy> {
  return api.get<Policy>(`/policies/${encodeURIComponent(policyId)}`);
}

export function createPolicy(input: PolicyInput): Promise<PolicyVersionEnvelope> {
  return api.post<PolicyVersionEnvelope>("/policies", input);
}

export function updatePolicy(policyId: ID, input: PolicyInput): Promise<PolicyVersionEnvelope> {
  return api.put<PolicyVersionEnvelope>(`/policies/${encodeURIComponent(policyId)}`, input);
}

export function deletePolicy(policyId: ID): Promise<void> {
  return api.delete<void>(`/policies/${encodeURIComponent(policyId)}`);
}

export function setPolicyEnabled(policyId: ID, enabled: boolean): Promise<Policy> {
  return api.post<Policy>(`/policies/${encodeURIComponent(policyId)}/${enabled ? "enable" : "disable"}`);
}

export function listVersions(policyId: ID): Promise<VersionList> {
  return api.get<VersionList>(`/policies/${encodeURIComponent(policyId)}/versions`);
}

export function getVersion(policyId: ID, version: number): Promise<PolicyVersion> {
  return api.get<PolicyVersion>(`/policies/${encodeURIComponent(policyId)}/versions/${version}`);
}

export interface PublishVersionInput {
  targets: TargetInput[];
  compression: { algorithm: string; level: number };
  encryption: { enabled: boolean; key_ref: string };
  retention: { keep_last_n: number; keep_days: number; keep_minimum: number };
  storage_id?: string | undefined;
  note?: string | undefined;
}

export function publishVersion(policyId: ID, input: PublishVersionInput): Promise<PolicyVersion> {
  return api.post<PolicyVersion>(`/policies/${encodeURIComponent(policyId)}/versions`, input);
}

export function listTargets(policyId: ID): Promise<TargetList> {
  return api.get<TargetList>(`/policies/${encodeURIComponent(policyId)}/targets`);
}

export function addTarget(policyId: ID, input: TargetInput): Promise<PolicyVersion> {
  return api.post<PolicyVersion>(`/policies/${encodeURIComponent(policyId)}/targets`, input);
}

export function updateTarget(policyId: ID, targetId: ID, input: TargetInput): Promise<PolicyVersion> {
  return api.put<PolicyVersion>(
    `/policies/${encodeURIComponent(policyId)}/targets/${encodeURIComponent(targetId)}`,
    input,
  );
}

export function deleteTarget(policyId: ID, targetId: ID): Promise<PolicyVersion> {
  return api.delete<PolicyVersion>(
    `/policies/${encodeURIComponent(policyId)}/targets/${encodeURIComponent(targetId)}`,
  );
}

// --- Schedules ---

export function listSchedules(policyId?: ID): Promise<ScheduleList> {
  return api.get<ScheduleList>(`/schedules${qs({ policy_id: policyId })}`);
}

export function createSchedule(input: ScheduleInput): Promise<Schedule> {
  return api.post<Schedule>("/schedules", input);
}

export function updateSchedule(scheduleId: ID, input: ScheduleInput): Promise<Schedule> {
  return api.put<Schedule>(`/schedules/${encodeURIComponent(scheduleId)}`, input);
}

export function deleteSchedule(scheduleId: ID): Promise<void> {
  return api.delete<void>(`/schedules/${encodeURIComponent(scheduleId)}`);
}

export function pauseSchedule(scheduleId: ID): Promise<Schedule> {
  return api.post<Schedule>(`/schedules/${encodeURIComponent(scheduleId)}/pause`);
}

export function resumeSchedule(scheduleId: ID): Promise<Schedule> {
  return api.post<Schedule>(`/schedules/${encodeURIComponent(scheduleId)}/resume`);
}

export function runSchedule(scheduleId: ID, agentId?: string): Promise<{ jobs: ManualRunJob[] }> {
  return api.post<{ jobs: ManualRunJob[] }>(
    `/schedules/${encodeURIComponent(scheduleId)}/run`,
    agentId === undefined || agentId === "" ? {} : { agent_id: agentId },
  );
}

// --- Reference lists for selects (same endpoints, local types) ---

interface AgentListResponse {
  items: AgentOption[];
  total: number;
}

export function listAgentOptions(): Promise<AgentOption[]> {
  return api
    .get<AgentListResponse>(`/agents${qs({ limit: 200 })}`)
    .then((res) => res.items);
}

export function listStorageOptions(): Promise<StorageOption[]> {
  return api.get<StorageOption[]>("/storage");
}

export function listCredentialOptions(): Promise<CredentialOption[]> {
  return api.get<CredentialOption[]>("/credentials");
}

export type { PolicyTarget };
