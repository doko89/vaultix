import type { ID } from "@/shared/types";

// Storage shapes from internal/adapter/http/storage (dto.go). Lists are
// bare arrays; test-connection answers 200 with ok:false on probe failure.

export interface StorageDestination {
  id: ID;
  name: string;
  endpoint: string;
  region: string;
  bucket: string;
  prefix: string;
  credential_ref: string;
  use_path_style: boolean;
  default: boolean;
  created_at: string;
  updated_at: string;
}

export interface DestinationInput {
  name: string;
  endpoint?: string | undefined;
  region?: string | undefined;
  bucket: string;
  prefix?: string | undefined;
  credential_ref?: string | undefined;
  use_path_style: boolean;
  default: boolean;
}

export interface TestConnectionResult {
  destination_id: ID;
  ok: boolean;
  error?: string;
  checked_at: string;
}

/** Minimal credential view for the credential_ref select (GET /credentials). */
export interface StorageCredentialOption {
  id: ID;
  name: string;
  kind: string;
  key_ref: string;
}

export function validateDestination(input: DestinationInput): string[] {
  const errors: string[] = [];
  if (input.name.trim() === "") errors.push("Name is required.");
  if (input.bucket.trim().length < 3) errors.push("Bucket must be at least 3 characters.");
  if (input.endpoint !== undefined && input.endpoint !== "") {
    try {
      const url = new URL(input.endpoint);
      if (url.protocol !== "http:" && url.protocol !== "https:") {
        errors.push("Endpoint must be an http(s) URL.");
      }
    } catch {
      errors.push("Endpoint must be a valid http(s) URL.");
    }
  }
  return errors;
}
