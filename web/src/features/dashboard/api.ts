import { api, qs } from "@/shared/lib/api";
import type { Dashboard } from "./types";

export function getDashboard(
  opts: { recent_limit?: number; gap_days?: number; trend_days?: number } = {},
): Promise<Dashboard> {
  return api.get<Dashboard>(
    `/dashboard${qs({ recent_limit: opts.recent_limit, gap_days: opts.gap_days, trend_days: opts.trend_days })}`,
  );
}

// Back-compat alias for the scaffold API name.
export const getDashboardSummary = getDashboard;
