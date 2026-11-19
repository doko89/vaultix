import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { ID } from "@/shared/types";
import { errorText } from "@/shared/lib/api";
import { useToasts } from "@/shared/ui/toast";
import { cancelJob, createManualJob, getJob, listJobs, retryJob } from "./api";
import type { JobFilters } from "./types";
import { isActiveState } from "./types";

export const JOBS_KEY = ["jobs"] as const;

const POLL_MS = 5_000;

export function useJobs(filters: JobFilters = {}) {
  const { agent_id, policy_id, state, since, until, limit, offset } = filters;
  return useQuery({
    queryKey: [...JOBS_KEY, "list", agent_id ?? "", policy_id ?? "", state ?? "", since ?? "", until ?? "", limit ?? 25, offset ?? 0],
    queryFn: () => listJobs({ agent_id, policy_id, state, since, until, limit, offset }),
    // Poll in place while any visible job is active (same query key, so
    // react-query swaps rows without scroll jumps or filter resets).
    refetchInterval: (query) => {
      const items = query.state.data?.items ?? [];
      return items.some((j) => isActiveState(j.state)) ? POLL_MS : false;
    },
  });
}

export function useJob(jobId: ID | undefined, pollWhenActive = true) {
  const query = useQuery({
    queryKey: [...JOBS_KEY, "detail", jobId ?? ""],
    queryFn: () => {
      if (jobId === undefined) throw new Error("job id is required");
      return getJob(jobId);
    },
    enabled: jobId !== undefined,
    refetchInterval: (query) => {
      if (!pollWhenActive) return false;
      const data = query.state.data;
      return data !== undefined && isActiveState(data.state) ? POLL_MS : false;
    },
  });
  return query;
}

function invalidateJobs(queryClient: ReturnType<typeof useQueryClient>) {
  void queryClient.invalidateQueries({ queryKey: [...JOBS_KEY] });
}

export function useCreateManualJob(onDone: () => void) {
  const queryClient = useQueryClient();
  const { push } = useToasts();
  return useMutation({
    mutationFn: ({ agentId, policyId }: { agentId: string; policyId: string }) => createManualJob(agentId, policyId),
    onSuccess: (job) => {
      push({ tone: "success", title: `Manual backup queued (${job.id.slice(0, 8)}…)` });
      invalidateJobs(queryClient);
      onDone();
    },
    onError: (err) => push({ tone: "error", title: "Could not queue backup", message: errorText(err) }),
  });
}

export function useRetryJob() {
  const queryClient = useQueryClient();
  const { push } = useToasts();
  return useMutation({
    mutationFn: (jobId: ID) => retryJob(jobId),
    onSuccess: (job) => {
      push({ tone: "success", title: `Retry queued as job ${job.id.slice(0, 8)}…` });
      invalidateJobs(queryClient);
    },
    onError: (err) => push({ tone: "error", title: "Could not retry job", message: errorText(err) }),
  });
}

export function useCancelJob() {
  const queryClient = useQueryClient();
  const { push } = useToasts();
  return useMutation({
    mutationFn: (jobId: ID) => cancelJob(jobId),
    onSuccess: () => {
      push({ tone: "success", title: "Job cancelled" });
      invalidateJobs(queryClient);
    },
    onError: (err) => push({ tone: "error", title: "Could not cancel job", message: errorText(err) }),
  });
}
