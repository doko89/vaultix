import { useQuery } from "@tanstack/react-query";
import { getGlobalSettings, listOidcProviders } from "./api";

export function useGlobalSettings() {
  return useQuery({ queryKey: ["settings"], queryFn: getGlobalSettings });
}

export function useOidcProviders() {
  return useQuery({ queryKey: ["settings", "oidc-providers"], queryFn: listOidcProviders });
}
