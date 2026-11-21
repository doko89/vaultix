import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { ID } from "@/shared/types";
import { errorText } from "@/shared/lib/api";
import { useToasts } from "@/shared/ui/toast";
import {
  deleteAgent,
  getAgent,
  issueEnrollToken,
  listAgentPolicies,
  listAgentResources,
  listAgents,
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
