import { api, qs } from "@/shared/lib/api";
import type { ID } from "@/shared/types";
import type { Job, JobDetail, JobFilters, JobList } from "./types";

// NOTE: the jobs slice mounts under chi Route("/api/jobs") with trailing
// "/" patterns (jobs.go Mount), so list/create keep the trailing slash.

export function listJobs(filters: JobFilters = {}): Promise<JobList> {
  return api.get<JobList>(
    `/jobs/${qs({
      agent_id: emptyToUndefined(filters.agent_id),
      policy_id: emptyToUndefined(filters.policy_id),
      state: emptyToUndefined(filters.state),
      since: emptyToUndefined(filters.since),
      until: emptyToUndefined(filters.until),
      limit: filters.limit,
      offset: filters.offset,
    })}`,
  );
}

export function getJob(jobId: ID): Promise<JobDetail> {
  return api.get<JobDetail>(`/jobs/${encodeURIComponent(jobId)}`);
}

export function createManualJob(agentId: string, policyId: string): Promise<Job> {
  return api.post<Job>("/jobs/", { agent_id: agentId, policy_id: policyId });
}

export function retryJob(jobId: ID): Promise<Job> {
  return api.post<Job>(`/jobs/${encodeURIComponent(jobId)}/retry`);
}

export function cancelJob(jobId: ID): Promise<Job> {
  return api.post<Job>(`/jobs/${encodeURIComponent(jobId)}/cancel`);
}

function emptyToUndefined(value: string | undefined): string | undefined {
  return value === undefined || value.trim() === "" ? undefined : value.trim();
}
