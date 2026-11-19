import type { ID } from "@/shared/types";

export type AuditOutcome = "success" | "failure";

export interface AuditEntry {
  id: ID;
  actor: string;
  action: string;
  target: string;
  timestamp: string;
  outcome: AuditOutcome;
}
