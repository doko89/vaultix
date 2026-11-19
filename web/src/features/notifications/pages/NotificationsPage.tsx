import { useEffect, useState } from "react";
import { Loader2, Plus } from "lucide-react";
import { errorText } from "@/shared/lib/api";
import { Button } from "@/shared/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/shared/ui/card";
import { ConfirmDialog } from "@/shared/ui/confirm-dialog";
import { Dialog } from "@/shared/ui/dialog";
import { Input } from "@/shared/ui/input";
import { PageHeader } from "@/shared/ui/page-header";
import { Select } from "@/shared/ui/select";
import { TableSkeleton } from "@/shared/ui/skeleton";
import {
  useCreateRule,
  useDeleteRule,
  useNotificationRules,
  useTestRule,
  useUpdateRule,
} from "../hooks";
import { CHANNEL_KINDS, NOTIFICATION_EVENTS, validateRule } from "../types";
import type { ChannelKind, NotificationRule, NotificationRuleInput } from "../types";

function RuleDialog({
  open,
  rule,
  onClose,
}: {
  open: boolean;
  rule?: NotificationRule | undefined;
  onClose: () => void;
}) {
  const [name, setName] = useState("");
  const [events, setEvents] = useState<string[]>([]);
  const [channel, setChannel] = useState<ChannelKind>("email");
  const [recipient, setRecipient] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [showErrors, setShowErrors] = useState(false);
  const create = useCreateRule(onClose);
  const update = useUpdateRule(onClose);

  useEffect(() => {
    if (!open) return;
    setShowErrors(false);
    setName(rule?.name ?? "");
    setEvents(rule?.events ?? []);
    setChannel((rule?.channel as ChannelKind) ?? "email");
    setRecipient(rule?.recipient ?? "");
    setEnabled(rule?.enabled ?? true);
  }, [open, rule]);

  const busy = create.isPending || update.isPending;
  const errors = showErrors ? validateRule({ name, events, channel, recipient, enabled }) : [];

  function toggleEvent(event: string, checked: boolean): void {
    setEvents((prev) => (checked ? [...prev, event] : prev.filter((e) => e !== event)));
  }

  function submit(): void {
    setShowErrors(true);
    const input: NotificationRuleInput = { name: name.trim(), events, channel, recipient: recipient.trim(), enabled };
    if (validateRule(input).length > 0) return;
    if (rule === undefined) {
      create.mutate(input);
    } else {
      update.mutate({ ruleId: rule.id, input });
    }
  }

  return (
    <Dialog
      open={open}
      title={rule === undefined ? "Add destination" : "Edit destination"}
      onClose={onClose}
    >
      <div className="flex flex-col gap-3">
        <div>
          <label className="mb-1 block text-sm font-medium">Name</label>
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="Ops on-call" />
        </div>
        <div>
          <span className="mb-1 block text-sm font-medium">Events</span>
          <div className="flex flex-wrap gap-3">
            {NOTIFICATION_EVENTS.map((event) => (
              <label key={event} className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={events.includes(event)}
                  onChange={(e) => toggleEvent(event, e.target.checked)}
                />
                <span className="font-mono text-xs">{event}</span>
              </label>
            ))}
          </div>
        </div>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div>
            <label className="mb-1 block text-sm font-medium">Channel</label>
            <Select value={channel} onChange={(e) => setChannel(e.target.value as ChannelKind)}>
              {CHANNEL_KINDS.map((c) => (
                <option key={c} value={c}>
                  {c}
                </option>
              ))}
            </Select>
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium">
              {channel === "email" ? "Email address" : "Webhook URL"}
            </label>
            <Input
              value={recipient}
              onChange={(e) => setRecipient(e.target.value)}
              placeholder={channel === "email" ? "ops@example.com" : "https://hooks.example.com/…"}
              className="font-mono"
            />
          </div>
        </div>
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
          Enabled
        </label>
        {errors.length > 0 && (
          <ul className="flex flex-col gap-1 text-xs text-destructive">
            {errors.map((e, i) => (
              <li key={i}>{e}</li>
            ))}
          </ul>
        )}
        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button type="button" onClick={submit} disabled={busy}>
            {busy ? "Saving…" : rule === undefined ? "Add destination" : "Save destination"}
          </Button>
        </div>
      </div>
    </Dialog>
  );
}

export function NotificationsPage() {
  const [dialog, setDialog] = useState<{ open: boolean; rule?: NotificationRule }>({ open: false });
  const [confirmDelete, setConfirmDelete] = useState<NotificationRule | undefined>(undefined);
  const [testingId, setTestingId] = useState<string | undefined>(undefined);
  const rules = useNotificationRules();
  const remove = useDeleteRule();
  const test = useTestRule();

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Notifications"
        description="Configure email and webhook destinations for failed jobs and agent changes."
        action={
          <Button type="button" onClick={() => setDialog({ open: true })}>
            <Plus aria-hidden />
            Add destination
          </Button>
        }
      />
      <Card>
        <CardHeader>
          <CardTitle>Destinations</CardTitle>
        </CardHeader>
        <CardContent>
          {rules.isLoading ? (
            <TableSkeleton rows={4} cols={4} />
          ) : rules.data === undefined ? (
            <p className="text-sm text-destructive">Could not load rules: {errorText(rules.error)}</p>
          ) : rules.data.items.length === 0 ? (
            <p className="text-sm text-gray-500">No destinations yet. Job failures will go unnoticed until you add one.</p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm">
                <thead>
                  <tr className="border-b border-border text-xs text-gray-500">
                    <th className="py-2 pr-4 font-medium">Name</th>
                    <th className="py-2 pr-4 font-medium">Events</th>
                    <th className="py-2 pr-4 font-medium">Channel</th>
                    <th className="py-2 pr-4 font-medium">State</th>
                    <th className="py-2 pr-4 font-medium">
                      <span className="sr-only">Actions</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {rules.data.items.map((r) => (
                    <tr key={r.id} className="border-b border-border">
                      <td className="py-2 pr-4 font-medium">{r.name}</td>
                      <td className="py-2 pr-4 font-mono text-xs">{r.events.join(", ")}</td>
                      <td className="py-2 pr-4 text-xs">
                        {r.channel} · <span className="font-mono">{r.recipient}</span>
                      </td>
                      <td className="py-2 pr-4">
                        {r.enabled ? (
                          <span className="inline-flex items-center rounded-full border border-success/30 bg-success/10 px-2.5 py-0.5 text-xs font-medium text-success">
                            Enabled
                          </span>
                        ) : (
                          <span className="inline-flex items-center rounded-full border border-border bg-surface px-2.5 py-0.5 text-xs font-medium text-gray-500">
                            Disabled
                          </span>
                        )}
                      </td>
                      <td className="py-2 pr-4">
                        <span className="flex flex-wrap gap-1">
                          <Button
                            type="button"
                            variant="ghost"
                            size="sm"
                            disabled={testingId === r.id}
                            onClick={() => {
                              setTestingId(r.id);
                              test.mutate(r.id, { onSettled: () => setTestingId(undefined) });
                            }}
                          >
                            {testingId === r.id && <Loader2 className="animate-spin" aria-hidden />}
                            Test
                          </Button>
                          <Button
                            type="button"
                            variant="ghost"
                            size="sm"
                            onClick={() => setDialog({ open: true, rule: r })}
                          >
                            Edit
                          </Button>
                          <Button type="button" variant="ghost" size="sm" onClick={() => setConfirmDelete(r)}>
                            Delete
                          </Button>
                        </span>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>

      <RuleDialog open={dialog.open} rule={dialog.rule} onClose={() => setDialog({ open: false })} />

      <ConfirmDialog
        open={confirmDelete !== undefined}
        title="Delete destination?"
        body={`“${confirmDelete?.name ?? ""}” stops receiving alerts immediately.`}
        confirmLabel="Delete destination"
        busy={remove.isPending}
        onConfirm={() => {
          if (confirmDelete !== undefined) {
            remove.mutate(confirmDelete.id, { onSuccess: () => setConfirmDelete(undefined) });
          }
        }}
        onClose={() => setConfirmDelete(undefined)}
      />
    </div>
  );
}
