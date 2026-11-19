import { useQuery } from "@tanstack/react-query";
import { listRestoreJobs } from "./api";

export function useRestoreJobs() {
  return useQuery({ queryKey: ["restores"], queryFn: listRestoreJobs });
}
