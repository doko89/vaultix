import { api } from "@/shared/lib/api";
import type { RestoreJob } from "./types";

export function listRestoreJobs(): Promise<RestoreJob[]> {
  return api.get<RestoreJob[]>("/restores");
}
