import { api } from "@/shared/lib/api";
import type { AuditEntry } from "./types";

export function listAuditEntries(): Promise<AuditEntry[]> {
  return api.get<AuditEntry[]>("/audit-logs");
}
