import type { ID } from "@/shared/types";

// OIDC provider as exposed to the UI: display name + availability only.
// Client secrets are never returned by read APIs (PRD section 7.1.1).
export interface OidcProvider {
  id: ID;
  name: string;
  enabled: boolean;
  available: boolean;
  unavailableReason?: string;
}

export interface GlobalSettings {
  localLoginEnabled: boolean;
}
