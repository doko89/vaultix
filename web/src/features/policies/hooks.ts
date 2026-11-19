import { useQuery } from "@tanstack/react-query";
import { listPolicies, listSchedules } from "./api";

export function usePolicies() {
  return useQuery({ queryKey: ["policies"], queryFn: listPolicies });
}

export function useSchedules() {
  return useQuery({ queryKey: ["schedules"], queryFn: listSchedules });
}
