import { api, qs } from "@/shared/lib/api";
import type { AuditFilters, AuditList } from "./types";

export function listAuditEntries(filters: AuditFilters = {}): Promise<AuditList> {
  return api.get<AuditList>(
    `/audit-logs${qs({
      actor: trimOpt(filters.actor),
      action: trimOpt(filters.action),
      target_type: trimOpt(filters.target_type),
      target_id: trimOpt(filters.target_id),
      outcome: trimOpt(filters.outcome),
      since: trimOpt(filters.since),
      until: trimOpt(filters.until),
      limit: filters.limit,
      offset: filters.offset,
    })}`,
  );
}

function trimOpt(value: string | undefined): string | undefined {
  if (value === undefined) return undefined;
  const t = value.trim();
  return t === "" ? undefined : t;
}
