import { api } from "@/shared/lib/api";
import type { Policy, Schedule } from "./types";

export function listPolicies(): Promise<Policy[]> {
  return api.get<Policy[]>("/policies");
}

export function listSchedules(): Promise<Schedule[]> {
  return api.get<Schedule[]>("/schedules");
}
