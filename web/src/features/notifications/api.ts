import { api } from "@/shared/lib/api";
import type { ID } from "@/shared/types";
import type { NotificationRule, NotificationRuleInput } from "./types";

export function listNotificationRules(): Promise<{ items: NotificationRule[] }> {
  return api.get<{ items: NotificationRule[] }>("/notifications/rules");
}

export function createNotificationRule(input: NotificationRuleInput): Promise<NotificationRule> {
  return api.post<NotificationRule>("/notifications/rules", input);
}

export function updateNotificationRule(ruleId: ID, input: NotificationRuleInput): Promise<NotificationRule> {
  return api.put<NotificationRule>(`/notifications/rules/${encodeURIComponent(ruleId)}`, input);
}

export function deleteNotificationRule(ruleId: ID): Promise<void> {
  return api.delete<void>(`/notifications/rules/${encodeURIComponent(ruleId)}`);
}

export function testNotificationRule(ruleId: ID): Promise<{ ok: boolean }> {
  return api.post<{ ok: boolean }>(`/notifications/rules/${encodeURIComponent(ruleId)}/test`);
}

// Back-compat alias for the scaffold API name.
export const listNotificationChannels = listNotificationRules;
