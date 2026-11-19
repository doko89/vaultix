import { api, qs } from "@/shared/lib/api";
import type { ID } from "@/shared/types";
import type {
  Confirmation,
  CurrentActor,
  PlanRequest,
  RestoreCatalogPage,
  RestoreJob,
  RestorePlan,
} from "./types";

export function listRestorableSnapshots(opts: { limit?: number; offset?: number } = {}): Promise<RestoreCatalogPage> {
  return api.getData<RestoreCatalogPage>(
    `/catalog${qs({ restorable_only: true, limit: opts.limit ?? 50, offset: opts.offset })}`,
  );
}

export function planRestore(req: PlanRequest): Promise<RestorePlan> {
  return api.postData<RestorePlan>("/restore/plan", req);
}

export function confirmRestore(planId: string, actor: string): Promise<Confirmation> {
  return api.postData<Confirmation>("/restore/confirm", { plan_id: planId, actor });
}

export function executeRestore(token: string, actor: string): Promise<RestoreJob> {
  return api.postData<RestoreJob>("/restore/execute", { token, actor });
}

export function restoreStatus(jobId: ID): Promise<RestoreJob> {
  return api.getData<RestoreJob>(`/restore/status${qs({ id: jobId })}`);
}

/** Local session fetch so this feature never imports features/auth. */
export function currentActor(): Promise<CurrentActor> {
  return api.get<CurrentActor>("/auth/me");
}
