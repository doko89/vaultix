import type { ID } from "@/shared/types";

// Notification rules from internal/adapter/http/platform (dto.go) and
// internal/usecase/notification (rules.go events).

export type ChannelKind = "email" | "webhook";

export const CHANNEL_KINDS: ChannelKind[] = ["email", "webhook"];

export const NOTIFICATION_EVENTS = ["job_failed", "agent_offline", "agent_stale", "agent_online"] as const;

export type NotificationEvent = (typeof NOTIFICATION_EVENTS)[number];

export interface NotificationRule {
  id: ID;
  name: string;
  events: string[];
  channel: string;
  recipient: string;
  enabled: boolean;
}

export interface NotificationRuleInput {
  name: string;
  events: string[];
  channel: ChannelKind;
  recipient: string;
  enabled: boolean;
}

// Back-compat alias for the scaffold type name.
export type NotificationChannel = NotificationRule;

export function validateRule(input: NotificationRuleInput): string[] {
  const errors: string[] = [];
  if (input.name.trim() === "") errors.push("Name is required.");
  if (input.events.length === 0) errors.push("Select at least one event.");
  if (input.recipient.trim() === "") {
    errors.push("Recipient is required.");
  } else if (input.channel === "email" && !input.recipient.includes("@")) {
    errors.push("Email recipient must be a valid address.");
  } else if (input.channel === "webhook") {
    try {
      const url = new URL(input.recipient);
      if (url.protocol !== "http:" && url.protocol !== "https:") errors.push("Webhook recipient must be an http(s) URL.");
    } catch {
      errors.push("Webhook recipient must be a valid http(s) URL.");
    }
  }
  return errors;
}
