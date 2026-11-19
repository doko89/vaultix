import { api } from "@/shared/lib/api";
import type { DashboardSummary } from "./types";

export function getDashboardSummary(): Promise<DashboardSummary> {
  return api.get<DashboardSummary>("/dashboard/summary");
}
