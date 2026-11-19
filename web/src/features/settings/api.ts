import { api } from "@/shared/lib/api";
import type { ID } from "@/shared/types";
import type { GlobalSettings, OidcProvider, OidcProviderInput } from "./types";

export function getGlobalSettings(): Promise<GlobalSettings> {
  return api.get<GlobalSettings>("/settings");
}

export function updateGlobalSettings(input: GlobalSettings): Promise<GlobalSettings> {
  return api.put<GlobalSettings>("/settings", input);
}

export function listOidcProviders(): Promise<{ items: OidcProvider[] }> {
  return api.get<{ items: OidcProvider[] }>("/settings/oidc-providers");
}

export function createOidcProvider(input: OidcProviderInput): Promise<OidcProvider> {
  return api.post<OidcProvider>("/settings/oidc-providers", input);
}

export function updateOidcProvider(providerId: ID, input: OidcProviderInput): Promise<OidcProvider> {
  return api.put<OidcProvider>(`/settings/oidc-providers/${encodeURIComponent(providerId)}`, input);
}

export function deleteOidcProvider(providerId: ID): Promise<void> {
  return api.delete<void>(`/settings/oidc-providers/${encodeURIComponent(providerId)}`);
}

export function testOidcProvider(providerId: ID): Promise<{ ok: boolean }> {
  return api.post<{ ok: boolean }>(
    `/settings/oidc-providers/${encodeURIComponent(providerId)}/test`,
    {},
    { timeoutMs: 30_000 },
  );
}
