import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { ID } from "@/shared/types";
import { errorText } from "@/shared/lib/api";
import { useToasts } from "@/shared/ui/toast";
import {
  createOidcProvider,
  deleteOidcProvider,
  getGlobalSettings,
  listOidcProviders,
  testOidcProvider,
  updateGlobalSettings,
  updateOidcProvider,
} from "./api";
import type { GlobalSettings, OidcProviderInput } from "./types";

export const SETTINGS_KEY = ["settings"] as const;

export function useGlobalSettings() {
  return useQuery({ queryKey: [...SETTINGS_KEY, "global"], queryFn: getGlobalSettings });
}

export function useUpdateGlobalSettings() {
  const queryClient = useQueryClient();
  const { push } = useToasts();
  return useMutation({
    mutationFn: (input: GlobalSettings) => updateGlobalSettings(input),
    onSuccess: () => {
      push({ tone: "success", title: "Settings saved" });
      void queryClient.invalidateQueries({ queryKey: [...SETTINGS_KEY] });
    },
    onError: (err) => push({ tone: "error", title: "Could not save settings", message: errorText(err) }),
  });
}

export function useOidcProviders() {
  return useQuery({ queryKey: [...SETTINGS_KEY, "oidc-providers"], queryFn: listOidcProviders });
}

export function useCreateOidcProvider(onDone: () => void) {
  const queryClient = useQueryClient();
  const { push } = useToasts();
  return useMutation({
    mutationFn: (input: OidcProviderInput) => createOidcProvider(input),
    onSuccess: (provider) => {
      push({ tone: "success", title: `Provider “${provider.name}” created` });
      void queryClient.invalidateQueries({ queryKey: [...SETTINGS_KEY] });
      onDone();
    },
    onError: (err) => push({ tone: "error", title: "Could not create provider", message: errorText(err) }),
  });
}

export function useUpdateOidcProvider(onDone: () => void) {
  const queryClient = useQueryClient();
  const { push } = useToasts();
  return useMutation({
    mutationFn: ({ providerId, input }: { providerId: ID; input: OidcProviderInput }) =>
      updateOidcProvider(providerId, input),
    onSuccess: (provider) => {
      push({ tone: "success", title: `Provider “${provider.name}” updated` });
      void queryClient.invalidateQueries({ queryKey: [...SETTINGS_KEY] });
      onDone();
    },
    onError: (err) => push({ tone: "error", title: "Could not update provider", message: errorText(err) }),
  });
}

export function useDeleteOidcProvider() {
  const queryClient = useQueryClient();
  const { push } = useToasts();
  return useMutation({
    mutationFn: (providerId: ID) => deleteOidcProvider(providerId),
    onSuccess: () => {
      push({ tone: "success", title: "Provider deleted" });
      void queryClient.invalidateQueries({ queryKey: [...SETTINGS_KEY] });
    },
    onError: (err) => push({ tone: "error", title: "Could not delete provider", message: errorText(err) }),
  });
}

export function useTestOidcProvider() {
  const { push } = useToasts();
  return useMutation({
    mutationFn: (providerId: ID) => testOidcProvider(providerId),
    onSuccess: () => push({ tone: "success", title: "Provider discovery + probe succeeded" }),
    onError: (err) => push({ tone: "error", title: "Provider test failed", message: errorText(err) }),
  });
}
