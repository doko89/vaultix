import { useQuery } from "@tanstack/react-query";
import type { ID } from "@/shared/types";
import { listAgentResources, listAgents } from "./api";

export function useAgents() {
  return useQuery({ queryKey: ["agents"], queryFn: listAgents });
}

export function useAgentResources(agentId: ID) {
  return useQuery({
    queryKey: ["agents", agentId, "resources"],
    queryFn: () => listAgentResources(agentId),
  });
}
