import type { ID } from "@/shared/types";

export type ChannelKind = "email" | "webhook";

export interface NotificationChannel {
  id: ID;
  kind: ChannelKind;
  name: string;
  enabled: boolean;
}
