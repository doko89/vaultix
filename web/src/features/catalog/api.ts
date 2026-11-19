import { api } from "@/shared/lib/api";
import type { ID } from "@/shared/types";
import type { Snapshot } from "./types";

export function listSnapshots(): Promise<Snapshot[]> {
  return api.get<Snapshot[]>("/snapshots");
}

export function getSnapshot(snapshotId: ID): Promise<Snapshot> {
  return api.get<Snapshot>(`/snapshots/${snapshotId}`);
}
