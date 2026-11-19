export type JobState =
  | "queued"
  | "assigned"
  | "running"
  | "uploading"
  | "verifying"
  | "succeeded"
  | "failed"
  | "cancelled";

export type AgentPresence = "online" | "offline" | "stale";

export type ID = string;

export interface Page<T> {
  items: T[];
  total: number;
  page: number;
  pageSize: number;
}
