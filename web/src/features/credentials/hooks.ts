import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { ID } from "@/shared/types";
import { errorText } from "@/shared/lib/api";
import { useToasts } from "@/shared/ui/toast";
import {
  createCredentialProfile,
  deleteCredentialProfile,
  listCredentialProfiles,
  updateCredentialProfile,
} from "./api";
import type { CredentialInput } from "./types";

export const CREDENTIALS_KEY = ["credentials"] as const;

export function useCredentialProfiles() {
  return useQuery({ queryKey: [...CREDENTIALS_KEY, "list"], queryFn: listCredentialProfiles });
}

export function useCreateCredentialProfile(onDone: () => void) {
  const queryClient = useQueryClient();
  const { push } = useToasts();
  return useMutation({
    mutationFn: (input: CredentialInput) => createCredentialProfile(input),
    onSuccess: (profile) => {
      push({ tone: "success", title: `Profile “${profile.name}” created` });
      void queryClient.invalidateQueries({ queryKey: [...CREDENTIALS_KEY] });
      onDone();
    },
    onError: (err) => push({ tone: "error", title: "Could not create profile", message: errorText(err) }),
  });
}

export function useUpdateCredentialProfile(onDone: () => void) {
  const queryClient = useQueryClient();
  const { push } = useToasts();
  return useMutation({
    mutationFn: ({ profileId, input }: { profileId: ID; input: CredentialInput }) =>
      updateCredentialProfile(profileId, input),
    onSuccess: (profile) => {
      push({ tone: "success", title: `Profile “${profile.name}” rotated` });
      void queryClient.invalidateQueries({ queryKey: [...CREDENTIALS_KEY] });
      onDone();
    },
    onError: (err) => push({ tone: "error", title: "Could not rotate profile", message: errorText(err) }),
  });
}

export function useDeleteCredentialProfile() {
  const queryClient = useQueryClient();
  const { push } = useToasts();
  return useMutation({
    mutationFn: (profileId: ID) => deleteCredentialProfile(profileId),
    onSuccess: () => {
      push({ tone: "success", title: "Profile deleted" });
      void queryClient.invalidateQueries({ queryKey: [...CREDENTIALS_KEY] });
    },
    onError: (err) => push({ tone: "error", title: "Could not delete profile", message: errorText(err) }),
  });
}
