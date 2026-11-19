import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { ID } from "@/shared/types";
import { errorText } from "@/shared/lib/api";
import { useToasts } from "@/shared/ui/toast";
import {
  createDestination,
  deleteDestination,
  listStorageCredentialOptions,
  listStorageDestinations,
  testStorageConnection,
  updateDestination,
} from "./api";
import type { DestinationInput } from "./types";

export const STORAGE_KEY = ["storage"] as const;

export function useStorageDestinations() {
  return useQuery({ queryKey: [...STORAGE_KEY, "list"], queryFn: listStorageDestinations });
}

export function useStorageCredentialOptions() {
  return useQuery({
    queryKey: [...STORAGE_KEY, "credential-options"],
    queryFn: listStorageCredentialOptions,
    staleTime: 60_000,
  });
}

export function useCreateDestination(onDone: () => void) {
  const queryClient = useQueryClient();
  const { push } = useToasts();
  return useMutation({
    mutationFn: (input: DestinationInput) => createDestination(input),
    onSuccess: (dest) => {
      push({ tone: "success", title: `Destination “${dest.name}” created` });
      void queryClient.invalidateQueries({ queryKey: [...STORAGE_KEY] });
      onDone();
    },
    onError: (err) => push({ tone: "error", title: "Could not create destination", message: errorText(err) }),
  });
}

export function useUpdateDestination(onDone: () => void) {
  const queryClient = useQueryClient();
  const { push } = useToasts();
  return useMutation({
    mutationFn: ({ destinationId, input }: { destinationId: ID; input: DestinationInput }) =>
      updateDestination(destinationId, input),
    onSuccess: (dest) => {
      push({ tone: "success", title: `Destination “${dest.name}” updated` });
      void queryClient.invalidateQueries({ queryKey: [...STORAGE_KEY] });
      onDone();
    },
    onError: (err) => push({ tone: "error", title: "Could not update destination", message: errorText(err) }),
  });
}

export function useDeleteDestination() {
  const queryClient = useQueryClient();
  const { push } = useToasts();
  return useMutation({
    mutationFn: (destinationId: ID) => deleteDestination(destinationId),
    onSuccess: () => {
      push({ tone: "success", title: "Destination deleted" });
      void queryClient.invalidateQueries({ queryKey: [...STORAGE_KEY] });
    },
    onError: (err) => push({ tone: "error", title: "Could not delete destination", message: errorText(err) }),
  });
}

export function useTestDestination() {
  return useMutation({
    mutationFn: (destinationId: ID) => testStorageConnection(destinationId),
  });
}
