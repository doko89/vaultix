import { useQuery } from "@tanstack/react-query";
import { listCredentialProfiles } from "./api";

export function useCredentialProfiles() {
  return useQuery({ queryKey: ["credentials"], queryFn: listCredentialProfiles });
}
