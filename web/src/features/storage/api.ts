import { api } from "@/shared/lib/api";
import type { ID } from "@/shared/types";
import type { StorageDestination } from "./types";

export function listStorageDestinations(): Promise<StorageDestination[]> {
  return api.get<StorageDestination[]>("/storage");
}

export function testStorageConnection(destinationId: ID): Promise<void> {
  return api.post<void>(`/storage/${destinationId}/test`);
}
