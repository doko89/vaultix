import { api, qs } from "@/shared/lib/api";
import type { ID } from "@/shared/types";
import type { CatalogFilters, CatalogPage, Manifest, SnapshotDetail } from "./types";

export function listSnapshots(filters: CatalogFilters = {}): Promise<CatalogPage> {
  return api.getData<CatalogPage>(
    `/catalog${qs({
      agent_id: trimOpt(filters.agent_id),
      policy_id: trimOpt(filters.policy_id),
      target: trimOpt(filters.target),
      since: trimOpt(filters.since),
      until: trimOpt(filters.until),
      restorable_only: filters.restorable_only === true ? true : undefined,
      limit: filters.limit,
      offset: filters.offset,
    })}`,
  );
}

export function getSnapshot(snapshotId: ID): Promise<SnapshotDetail> {
  return api.getData<SnapshotDetail>(`/catalog/snapshot${qs({ id: snapshotId })}`);
}

export function getManifest(snapshotId: ID): Promise<Manifest> {
  return api.getData<Manifest>(`/catalog/manifest${qs({ id: snapshotId })}`);
}

function trimOpt(value: string | undefined): string | undefined {
  if (value === undefined) return undefined;
  const t = value.trim();
  return t === "" ? undefined : t;
}
