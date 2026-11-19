import type { ID, JobState } from "@/shared/types";

export interface DashboardSummary {
  totalAgents: number;
  onlineAgents: number;
  offlineAgents: number;
  jobsByState: Record<JobState, number>;
  policiesWithoutRecentSuccess: ID[];
}
