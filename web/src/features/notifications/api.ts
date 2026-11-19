import { api } from "@/shared/lib/api";
import type { NotificationChannel } from "./types";

export function listNotificationChannels(): Promise<NotificationChannel[]> {
  return api.get<NotificationChannel[]>("/notifications/channels");
}
