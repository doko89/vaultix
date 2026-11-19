import type { ID, JobState } from "@/shared/types";

export interface JobEncryption {
  enabled: boolean;
  keyRef?: string;
}

export interface Job {
  id: ID;
  agentId: ID;
  policyId: ID;
  policyVersion: number;
  state: JobState;
  progressPct: number;
  bytesRead: number;
  bytesUploaded: number;
  encryption: JobEncryption;
  destination: string;
}
