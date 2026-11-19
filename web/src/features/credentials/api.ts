import { api } from "@/shared/lib/api";
import type { ID } from "@/shared/types";
import type { CredentialInput, CredentialProfile } from "./types";

export function listCredentialProfiles(): Promise<CredentialProfile[]> {
  return api.get<CredentialProfile[]>("/credentials");
}

export function createCredentialProfile(input: CredentialInput): Promise<CredentialProfile> {
  return api.post<CredentialProfile>("/credentials", input);
}

export function updateCredentialProfile(
  profileId: ID,
  input: CredentialInput,
): Promise<CredentialProfile> {
  return api.put<CredentialProfile>(`/credentials/${encodeURIComponent(profileId)}`, input);
}

export function deleteCredentialProfile(profileId: ID): Promise<void> {
  return api.delete<void>(`/credentials/${encodeURIComponent(profileId)}`);
}
