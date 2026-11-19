import type { ID } from "@/shared/types";

// Catalog shapes from internal/adapter/http/restore (dto.go). All restore
// endpoints wrap success payloads in {"data": ...} (helpers.go writeJSON).

export interface Snapshot {
  id: ID;
  agent_id: ID;
  policy_id: ID;
  policy_version: number;
  job_id: ID;
  target_type: string;
  selector: string;
  status: string;
  bytes_original?: number;
  bytes_stored?: number;
  object_count: number;
  encryption: "encrypted" | "unencrypted";
  key_ref?: string;
  manifest_id: ID;
  storage_id: ID;
  restorable: boolean;
  completed_at?: string;
  created_at: string;
}

export interface SnapshotObject {
  id: ID;
  object_key: string;
  kind: string;
  size: number;
  checksum: string;
  sequence: number;
}

export interface Manifest {
  id: ID;
  snapshot_id: ID;
  format_version: number;
  object_key: string;
  checksum: string;
  encryption: "encrypted" | "unencrypted";
  key_ref?: string;
  object_count: number;
  bytes_stored: number;
  verified_at?: string;
  created_at: string;
  objects: SnapshotObject[];
}

export interface CatalogPage {
  items: Snapshot[];
  total: number;
  limit: number;
  offset: number;
}

export interface SnapshotDetail {
  snapshot: Snapshot;
  manifest?: Manifest;
}

export interface CatalogFilters {
  agent_id?: string | undefined;
  policy_id?: string | undefined;
  target?: string | undefined;
  since?: string | undefined;
  until?: string | undefined;
  restorable_only?: boolean | undefined;
  limit?: number | undefined;
  offset?: number | undefined;
}

export function snapshotEncryptionLabel(s: Pick<Snapshot, "encryption" | "key_ref">): string {
  if (s.encryption !== "encrypted") return "Unencrypted";
  return s.key_ref !== undefined && s.key_ref !== "" ? `Encrypted · key ${s.key_ref}` : "Encrypted";
}
