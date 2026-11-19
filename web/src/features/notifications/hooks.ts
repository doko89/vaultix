import { useQuery } from "@tanstack/react-query";
import { listNotificationChannels } from "./api";

export function useNotificationChannels() {
  return useQuery({ queryKey: ["notifications", "channels"], queryFn: listNotificationChannels });
}
