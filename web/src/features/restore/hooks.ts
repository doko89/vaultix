import { useMutation, useQuery } from "@tanstack/react-query";
import type { ID } from "@/shared/types";
import {
  confirmRestore,
  currentActor,
  executeRestore,
  listRestorableSnapshots,
  planRestore,
  restoreStatus,
} from "./api";
import type { PlanRequest } from "./types";
import { isActiveRestoreState } from "./types";

export const RESTORE_KEY = ["restore"] as const;

const POLL_MS = 5_000;

export function useRestorableSnapshots() {
  return useQuery({
    queryKey: [...RESTORE_KEY, "snapshots"],
    queryFn: () => listRestorableSnapshots({ limit: 100 }),
  });
}

export function useCurrentActor() {
  return useQuery({ queryKey: [...RESTORE_KEY, "actor"], queryFn: currentActor, staleTime: 60_000 });
}

export function usePlanRestore() {
  return useMutation({ mutationFn: (req: PlanRequest) => planRestore(req) });
}

export function useConfirmRestore() {
  return useMutation({
    mutationFn: ({ planId, actor }: { planId: string; actor: string }) => confirmRestore(planId, actor),
  });
}

export function useExecuteRestore() {
  return useMutation({
    mutationFn: ({ token, actor }: { token: string; actor: string }) => executeRestore(token, actor),
  });
}

export function useRestoreStatus(jobId: ID | undefined) {
  return useQuery({
    queryKey: [...RESTORE_KEY, "status", jobId ?? ""],
    queryFn: () => {
      if (jobId === undefined) throw new Error("restore job id is required");
      return restoreStatus(jobId);
    },
    enabled: jobId !== undefined,
    refetchInterval: (query) => {
      const data = query.state.data;
      return data !== undefined && isActiveRestoreState(data.state) ? POLL_MS : false;
    },
  });
}
