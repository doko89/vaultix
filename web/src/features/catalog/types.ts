import type { ID } from "@/shared/types";

export interface Snapshot {
  id: ID;
  agentId: ID;
  policyId: ID;
  createdAt: string;
  encrypted: boolean;
  restorable: boolean;
}
