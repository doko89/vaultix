import { api } from "@/shared/lib/api";
import type { User } from "./types";

export function listUsers(): Promise<User[]> {
  return api.get<User[]>("/users");
}
