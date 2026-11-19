import type { ID } from "@/shared/types";

export type VaultixRole = "administrator" | "operator" | "readonly";

export interface User {
  id: ID;
  username: string;
  roles: VaultixRole[];
  oidcLinked: boolean;
}
