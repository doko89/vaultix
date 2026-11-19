import type { ID } from "@/shared/types";

// Secret values are never part of this type: the UI only handles
// references + metadata (DESIGN section 7, PRD section 7.7).
export interface CredentialProfile {
  id: ID;
  name: string;
  engine: string;
  createdAt: string;
}
