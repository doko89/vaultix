import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { ID } from "@/shared/types";
import { errorText } from "@/shared/lib/api";
import { useToasts } from "@/shared/ui/toast";
import {
  batchRequestUpdate,
  checkUpdates,
  deleteAgent,
  deleteArtifact,
  describeUpdate,
  getAgent,
  issueEnrollToken,
  listAgentPolicies,
  listAgentResources,
  listAgents,
  listArtifacts,
  requestUpdate,
  revokeAgent,
  setAgentDisabled,
} from "./api";
import type { AgentFilters } from "./api";

export const AGENTS_KEY = ["agents"] as const;

export function useAgents(filters: AgentFilters = {}) {
  const { status, q, limit, offset } = filters;
  return useQuery({
    queryKey: [...AGENTS_KEY, "list", status ?? "", q ?? "", limit ?? 25, offset ?? 0],
    queryFn: () => listAgents({ status, q, limit, offset }),
  });
}

export function useAgent(agentId: ID | undefined) {
  return useQuery({
    queryKey: [...AGENTS_KEY, "detail", agentId ?? ""],
    queryFn: () => {
      if (agentId === undefined) throw new Error("agent id is required");
      return getAgent(agentId);
    },
    enabled: agentId !== undefined,
  });
}

export function useIssueEnrollToken() {
  return useMutation({
    mutationFn: (ttlHours: number | undefined) => issueEnrollToken(ttlHours),
  });
}

export function useSetAgentDisabled() {
  const queryClient = useQueryClient();
  const { push } = useToasts();
  return useMutation({
    mutationFn: ({ agentId, disabled }: { agentId: ID; disabled: boolean }) =>
      setAgentDisabled(agentId, disabled),
    onSuccess: (agent) => {
      push({ tone: "success", title: agent.disabled ? "Agent disabled" : "Agent enabled" });
      void queryClient.invalidateQueries({ queryKey: [...AGENTS_KEY] });
    },
    onError: (err) => push({ tone: "error", title: "Could not update agent", message: errorText(err) }),
  });
}

export function useRevokeAgent() {
  const queryClient = useQueryClient();
  const { push } = useToasts();
  return useMutation({
    mutationFn: (agentId: ID) => revokeAgent(agentId),
    onSuccess: () => {
      push({ tone: "success", title: "Agent revoked" });
      void queryClient.invalidateQueries({ queryKey: [...AGENTS_KEY] });
    },
    onError: (err) => push({ tone: "error", title: "Could not revoke agent", message: errorText(err) }),
  });
}

export function useDeleteAgent(onDone: () => void) {
  const queryClient = useQueryClient();
  const { push } = useToasts();
  return useMutation({
    mutationFn: (agentId: ID) => deleteAgent(agentId),
    onSuccess: () => {
      push({ tone: "success", title: "Agent deleted" });
      void queryClient.invalidateQueries({ queryKey: [...AGENTS_KEY] });
      onDone();
    },
    onError: (err) => push({ tone: "error", title: "Could not delete agent", message: errorText(err) }),
  });
}

export function useAgentPolicies(agentId: ID | undefined) {
  return useQuery({
    queryKey: [...AGENTS_KEY, "policies", agentId ?? ""],
    queryFn: () => {
      if (agentId === undefined) throw new Error("agent id is required");
      return listAgentPolicies(agentId);
    },
    enabled: agentId !== undefined,
  });
}

export function useAgentResources(agentId: ID | undefined, kind?: string) {
  return useQuery({
    queryKey: [...AGENTS_KEY, "resources", agentId ?? "", kind ?? ""],
    queryFn: () => {
      if (agentId === undefined) throw new Error("agent id is required");
      return listAgentResources(agentId, kind === "" ? undefined : kind);
    },
    enabled: agentId !== undefined,
  });
}

export function useArtifacts() {
  return useQuery({
    queryKey: [...AGENTS_KEY, "artifacts"],
    queryFn: () => listArtifacts(),
  });
}

export function useCheckUpdates() {
  const queryClient = useQueryClient();
  const { push } = useToasts();
  return useMutation({
    mutationFn: () => checkUpdates(),
    onSuccess: (res) => {
      if (res.up_to_date) {
        push({ tone: "success", title: "Agent binaries are up to date" });
      } else {
        const names = res.cached.map((a) => `${a.version} (${a.goos}/${a.goarch})`).join(", ");
        push({ tone: "success", title: `Cached new binaries: ${names}` });
      }
      void queryClient.invalidateQueries({ queryKey: [...AGENTS_KEY] });
    },
    onError: (err) => push({ tone: "error", title: "Could not check for updates", message: errorText(err) }),
  });
}

export function useDeleteArtifact() {
  const queryClient = useQueryClient();
  const { push } = useToasts();
  return useMutation({
    mutationFn: (artifactId: ID) => deleteArtifact(artifactId),
    onSuccess: () => {
      push({ tone: "success", title: "Artifact deleted" });
      void queryClient.invalidateQueries({ queryKey: [...AGENTS_KEY] });
    },
    onError: (err) => push({ tone: "error", title: "Could not delete artifact", message: errorText(err) }),
  });
}

export function useUpdateDescribe(agentId: ID | undefined, pollWhenPending = true) {
  const query = useQuery({
    queryKey: [...AGENTS_KEY, "update", agentId ?? ""],
    queryFn: () => {
      if (agentId === undefined) throw new Error("agent id is required");
      return describeUpdate(agentId);
    },
    enabled: agentId !== undefined,
    refetchInterval: (query) => {
      if (!pollWhenPending) return false;
      return query.state.data?.pending?.status === "pending" ? 5_000 : false;
    },
  });
  return query;
}

export function useRequestUpdate() {
  const queryClient = useQueryClient();
  const { push } = useToasts();
  return useMutation({
    mutationFn: ({ agentId, artifactId }: { agentId: ID; artifactId: ID }) => requestUpdate(agentId, artifactId),
    onSuccess: (slot) => {
      push({ tone: "success", title: `Update to ${slot.target_version} requested` });
      void queryClient.invalidateQueries({ queryKey: [...AGENTS_KEY] });
    },
    onError: (err) => push({ tone: "error", title: "Could not request update", message: errorText(err) }),
  });
}

export function useBatchRequestUpdate(onDone: () => void) {
  const queryClient = useQueryClient();
  const { push } = useToasts();
  return useMutation({
    mutationFn: (agentIds: ID[]) => batchRequestUpdate(agentIds),
    onSuccess: (res) => {
      const requested = res.results.filter((r) => r.status === "requested").length;
      const skipped = res.results.filter((r) => r.status === "skipped").length;
      const failed = res.results.filter((r) => r.status === "failed");
      if (failed.length > 0) {
        push({
          tone: "error",
          title: `Batch update: ${requested} requested, ${skipped} skipped, ${failed.length} failed`,
          message: failed.map((r) => `${r.agent_id.slice(0, 8)}: ${r.error}`).join("; "),
        });
      } else {
        push({
          tone: "success",
          title: `Batch update: ${requested} requested, ${skipped} skipped, 0 failed`,
        });
      }
      void queryClient.invalidateQueries({ queryKey: [...AGENTS_KEY] });
      onDone();
    },
    onError: (err) => push({ tone: "error", title: "Could not request batch update", message: errorText(err) }),
  });
}
