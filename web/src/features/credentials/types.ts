import type { ID } from "@/shared/types";

// Secret profile shapes from internal/adapter/http/storage (dto.go).
// Secret values are never part of read types: the UI only handles
// references + metadata (DESIGN section 7, PRD section 7.7).

export type SecretKind = "database" | "storage" | "encryption" | "generic";

export const SECRET_KINDS: SecretKind[] = ["database", "storage", "encryption", "generic"];

export interface CredentialProfile {
  id: ID;
  name: string;
  kind: string;
  key_ref: string;
  created_by: string;
  created_at: string;
  updated_at: string;
}

export interface CredentialInput {
  name: string;
  kind: SecretKind;
  /** Write-only plaintext: sent on create/update, never read back. */
  plaintext: string;
}

export function validateCredential(input: CredentialInput): string[] {
  const errors: string[] = [];
  if (input.name.trim() === "") errors.push("Name is required.");
  if (input.plaintext === "") errors.push("Secret value is required.");
  return errors;
}
