import { api } from "@/shared/lib/api";
import type { ID } from "@/shared/types";
import type {
  DestinationInput,
  StorageCredentialOption,
  StorageDestination,
  TestConnectionResult,
} from "./types";

export function listStorageDestinations(): Promise<StorageDestination[]> {
  return api.get<StorageDestination[]>("/storage");
}

export function createDestination(input: DestinationInput): Promise<StorageDestination> {
  return api.post<StorageDestination>("/storage", input);
}

export function updateDestination(destinationId: ID, input: DestinationInput): Promise<StorageDestination> {
  return api.put<StorageDestination>(`/storage/${encodeURIComponent(destinationId)}`, input);
}

export function deleteDestination(destinationId: ID): Promise<void> {
  return api.delete<void>(`/storage/${encodeURIComponent(destinationId)}`);
}

export function testStorageConnection(destinationId: ID): Promise<TestConnectionResult> {
  return api.post<TestConnectionResult>(
    `/storage/${encodeURIComponent(destinationId)}/test`,
    {},
    { timeoutMs: 30_000 },
  );
}

export function listStorageCredentialOptions(): Promise<StorageCredentialOption[]> {
  return api.get<StorageCredentialOption[]>("/credentials");
}
