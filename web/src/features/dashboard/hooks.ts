import { useQuery } from "@tanstack/react-query";
import { getDashboard } from "./api";

export const DASHBOARD_KEY = ["dashboard"] as const;

export function useDashboard() {
  return useQuery({
    queryKey: [...DASHBOARD_KEY],
    queryFn: () => getDashboard({ recent_limit: 10, gap_days: 7, trend_days: 14 }),
    refetchInterval: 30_000,
  });
}

// Back-compat alias for the scaffold hook name.
export const useDashboardSummary = useDashboard;
