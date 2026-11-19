import type { ID } from "@/shared/types";

export interface StorageDestination {
  id: ID;
  name: string;
  endpoint: string;
  region: string;
  bucket: string;
  prefix: string;
}
