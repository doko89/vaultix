import { useQuery } from "@tanstack/react-query";
import { listAuditEntries } from "./api";

export function useAuditEntries() {
  return useQuery({ queryKey: ["audit-logs"], queryFn: listAuditEntries });
}
