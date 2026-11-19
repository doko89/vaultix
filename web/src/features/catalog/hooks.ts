import { useQuery } from "@tanstack/react-query";
import type { ID } from "@/shared/types";
import { getManifest, getSnapshot, listSnapshots } from "./api";
import type { CatalogFilters } from "./types";

export const CATALOG_KEY = ["catalog"] as const;

export function useSnapshots(filters: CatalogFilters = {}) {
  const { agent_id, policy_id, target, since, until, restorable_only, limit, offset } = filters;
  return useQuery({
    queryKey: [
      ...CATALOG_KEY,
      "list",
      agent_id ?? "",
      policy_id ?? "",
      target ?? "",
      since ?? "",
      until ?? "",
      restorable_only === true ? "1" : "0",
      limit ?? 25,
      offset ?? 0,
    ],
    queryFn: () =>
      listSnapshots({ agent_id, policy_id, target, since, until, restorable_only, limit, offset }),
  });
}

export function useSnapshot(snapshotId: ID | undefined) {
  return useQuery({
    queryKey: [...CATALOG_KEY, "detail", snapshotId ?? ""],
    queryFn: () => {
      if (snapshotId === undefined) throw new Error("snapshot id is required");
      return getSnapshot(snapshotId);
    },
    enabled: snapshotId !== undefined,
  });
}

export function useManifest(snapshotId: ID | undefined, enabled: boolean) {
  return useQuery({
    queryKey: [...CATALOG_KEY, "manifest", snapshotId ?? ""],
    queryFn: () => {
      if (snapshotId === undefined) throw new Error("snapshot id is required");
      return getManifest(snapshotId);
    },
    enabled: snapshotId !== undefined && enabled,
  });
}
