import { useQuery } from "@tanstack/react-query";
import type { ID } from "@/shared/types";
import { getJob, listJobs } from "./api";

export function useJobs() {
  return useQuery({ queryKey: ["jobs"], queryFn: listJobs });
}

export function useJob(jobId: ID) {
  return useQuery({ queryKey: ["jobs", jobId], queryFn: () => getJob(jobId) });
}
