import { useQuery } from "@tanstack/react-query";
import { listAuditEntries } from "./api";
import type { AuditFilters } from "./types";

export const AUDIT_KEY = ["audit-logs"] as const;

export function useAuditEntries(filters: AuditFilters = {}) {
  const { actor, action, target_type, target_id, outcome, since, until, limit, offset } = filters;
  return useQuery({
    queryKey: [
      ...AUDIT_KEY,
      "list",
      actor ?? "",
      action ?? "",
      target_type ?? "",
      target_id ?? "",
      outcome ?? "",
      since ?? "",
      until ?? "",
      limit ?? 25,
      offset ?? 0,
    ],
    queryFn: () => listAuditEntries({ actor, action, target_type, target_id, outcome, since, until, limit, offset }),
  });
}
