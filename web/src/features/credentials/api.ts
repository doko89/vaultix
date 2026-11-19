import { api } from "@/shared/lib/api";
import type { CredentialProfile } from "./types";

export function listCredentialProfiles(): Promise<CredentialProfile[]> {
  return api.get<CredentialProfile[]>("/credentials");
}
