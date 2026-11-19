import type { ID, JobState } from "@/shared/types";

export interface RestoreJob {
  id: ID;
  snapshotId: ID;
  state: JobState;
  targetPath: string;
}
