import { api } from "@/shared/lib/api";
import type { GlobalSettings, OidcProvider } from "./types";

export function getGlobalSettings(): Promise<GlobalSettings> {
  return api.get<GlobalSettings>("/settings");
}

export function listOidcProviders(): Promise<OidcProvider[]> {
  return api.get<OidcProvider[]>("/settings/oidc-providers");
}
