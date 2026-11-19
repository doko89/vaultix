import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { ID } from "@/shared/types";
import { errorText } from "@/shared/lib/api";
import { useToasts } from "@/shared/ui/toast";
import {
  createNotificationRule,
  deleteNotificationRule,
  listNotificationRules,
  testNotificationRule,
  updateNotificationRule,
} from "./api";
import type { NotificationRuleInput } from "./types";

export const NOTIFICATIONS_KEY = ["notifications"] as const;

export function useNotificationRules() {
  return useQuery({ queryKey: [...NOTIFICATIONS_KEY, "rules"], queryFn: listNotificationRules });
}

// Back-compat alias for the scaffold hook name.
export const useNotificationChannels = useNotificationRules;

export function useCreateRule(onDone: () => void) {
  const queryClient = useQueryClient();
  const { push } = useToasts();
  return useMutation({
    mutationFn: (input: NotificationRuleInput) => createNotificationRule(input),
    onSuccess: (rule) => {
      push({ tone: "success", title: `Rule “${rule.name}” created` });
      void queryClient.invalidateQueries({ queryKey: [...NOTIFICATIONS_KEY] });
      onDone();
    },
    onError: (err) => push({ tone: "error", title: "Could not create rule", message: errorText(err) }),
  });
}

export function useUpdateRule(onDone: () => void) {
  const queryClient = useQueryClient();
  const { push } = useToasts();
  return useMutation({
    mutationFn: ({ ruleId, input }: { ruleId: ID; input: NotificationRuleInput }) => updateNotificationRule(ruleId, input),
    onSuccess: (rule) => {
      push({ tone: "success", title: `Rule “${rule.name}” updated` });
      void queryClient.invalidateQueries({ queryKey: [...NOTIFICATIONS_KEY] });
      onDone();
    },
    onError: (err) => push({ tone: "error", title: "Could not update rule", message: errorText(err) }),
  });
}

export function useDeleteRule() {
  const queryClient = useQueryClient();
  const { push } = useToasts();
  return useMutation({
    mutationFn: (ruleId: ID) => deleteNotificationRule(ruleId),
    onSuccess: () => {
      push({ tone: "success", title: "Rule deleted" });
      void queryClient.invalidateQueries({ queryKey: [...NOTIFICATIONS_KEY] });
    },
    onError: (err) => push({ tone: "error", title: "Could not delete rule", message: errorText(err) }),
  });
}

export function useTestRule() {
  const { push } = useToasts();
  return useMutation({
    mutationFn: (ruleId: ID) => testNotificationRule(ruleId),
    onSuccess: () => push({ tone: "success", title: "Test notification sent" }),
    onError: (err) => push({ tone: "error", title: "Test failed", message: errorText(err) }),
  });
}
