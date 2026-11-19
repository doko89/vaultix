import { api } from "@/shared/lib/api";
import type { ID } from "@/shared/types";
import type { Job } from "./types";

export function listJobs(): Promise<Job[]> {
  return api.get<Job[]>("/jobs");
}

export function getJob(jobId: ID): Promise<Job> {
  return api.get<Job>(`/jobs/${jobId}`);
}

export function retryJob(jobId: ID): Promise<Job> {
  return api.post<Job>(`/jobs/${jobId}/retry`);
}

export function cancelJob(jobId: ID): Promise<Job> {
  return api.post<Job>(`/jobs/${jobId}/cancel`);
}
