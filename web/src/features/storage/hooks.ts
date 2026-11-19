import { useQuery } from "@tanstack/react-query";
import { listStorageDestinations } from "./api";

export function useStorageDestinations() {
  return useQuery({ queryKey: ["storage"], queryFn: listStorageDestinations });
}
