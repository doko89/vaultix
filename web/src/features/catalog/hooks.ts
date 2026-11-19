import { useQuery } from "@tanstack/react-query";
import type { ID } from "@/shared/types";
import { getSnapshot, listSnapshots } from "./api";

export function useSnapshots() {
  return useQuery({ queryKey: ["snapshots"], queryFn: listSnapshots });
}

export function useSnapshot(snapshotId: ID) {
  return useQuery({
    queryKey: ["snapshots", snapshotId],
    queryFn: () => getSnapshot(snapshotId),
  });
}
