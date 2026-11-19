import type { ID } from "@/shared/types";

export interface Policy {
  id: ID;
  name: string;
  description: string;
  enabled: boolean;
  version: number;
}

export type ScheduleKind = "one-time" | "hourly" | "daily" | "weekly";

export interface Schedule {
  id: ID;
  policyId: ID;
  kind: ScheduleKind;
  timezone: string;
  paused: boolean;
}
