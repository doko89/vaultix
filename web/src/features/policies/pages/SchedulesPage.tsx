import { useEffect, useState } from "react";
import { Plus } from "lucide-react";
import type { ID } from "@/shared/types";
import { errorText } from "@/shared/lib/api";
import { formatDateTime } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/shared/ui/card";
import { ConfirmDialog } from "@/shared/ui/confirm-dialog";
import { Dialog } from "@/shared/ui/dialog";
import { Input } from "@/shared/ui/input";
import { PageHeader } from "@/shared/ui/page-header";
import { Select } from "@/shared/ui/select";
import { TableSkeleton } from "@/shared/ui/skeleton";
import {
  useCreateSchedule,
  useDeleteSchedule,
  usePauseResumeSchedule,
  usePolicies,
  useRunSchedule,
  useSchedules,
  useUpdateSchedule,
} from "../hooks";
import { SCHEDULE_KIND_LABEL, SCHEDULE_KINDS } from "../types";
import type { Schedule, ScheduleInput, ScheduleKind } from "../types";

// Schedule validation mirrors the DTO checks (policies/dto.go checkTiming):
// kind ↔ field coherence, HH:MM times, weekday 0–6, IANA timezone.

function validateSchedule(input: ScheduleInput): string[] {
  const errors: string[] = [];
  if (input.policy_id.trim() === "") errors.push("Policy is required.");
  if (input.timezone.trim() === "") {
    errors.push("Timezone is required (IANA name, e.g. UTC or Asia/Jakarta).");
  } else {
    try {
      new Intl.DateTimeFormat("en", { timeZone: input.timezone });
    } catch {
      errors.push(`Unknown timezone ${input.timezone}.`);
    }
  }
  const timeOk = input.time_of_day !== undefined && /^([01]\d|2[0-3]):[0-5]\d$/.test(input.time_of_day);
  switch (input.kind) {
    case "one_time":
      if (input.run_at === undefined || input.run_at === "") errors.push("Run-at time is required for one-time schedules.");
      break;
    case "hourly":
      if (input.interval_hours === undefined || input.interval_hours <= 0) {
        errors.push("Interval hours must be positive for hourly schedules.");
      }
      break;
    case "daily":
      if (!timeOk) errors.push("Time of day must be HH:MM (24h) for daily schedules.");
      break;
    case "weekly":
      if (!timeOk) errors.push("Time of day must be HH:MM (24h) for weekly schedules.");
      if (input.weekday === undefined || input.weekday < 0 || input.weekday > 6) {
        errors.push("Weekday must be 0–6 (0 = Sunday) for weekly schedules.");
      }
      break;
    case "cron":
      if ((input.cron_expr ?? "").trim() === "") errors.push("Cron expression is required.");
      break;
  }
  if (input.max_concurrency !== undefined && input.max_concurrency < 0) {
    errors.push("Max concurrency must be >= 0 (0 = unlimited).");
  }
  return errors;
}

function blankSchedule(policyId: string): ScheduleInput {
  return { policy_id: policyId, kind: "daily", time_of_day: "02:00", timezone: "UTC", max_concurrency: 1 };
}

const WEEKDAYS = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"] as const;

function ScheduleDialog({
  open,
  policyId,
  policies,
  schedule,
  onClose,
}: {
  open: boolean;
  policyId: string;
  policies: { id: ID; name: string }[];
  schedule?: Schedule | undefined;
  onClose: () => void;
}) {
  const [form, setForm] = useState<ScheduleInput>(blankSchedule(policyId));
  const [showErrors, setShowErrors] = useState(false);
  const create = useCreateSchedule(onClose);
  const update = useUpdateSchedule(onClose);

  useEffect(() => {
    if (!open) return;
    setShowErrors(false);
    if (schedule === undefined) {
      setForm(blankSchedule(policyId));
      return;
    }
    const next: ScheduleInput = {
      policy_id: schedule.policy_id,
      kind: schedule.kind as ScheduleKind,
      timezone: schedule.timezone,
      max_concurrency: schedule.max_concurrency,
    };
    if (schedule.time_of_day !== undefined && schedule.time_of_day !== "") next.time_of_day = schedule.time_of_day;
    if (schedule.weekday !== undefined) next.weekday = schedule.weekday;
    if (schedule.interval_hours !== undefined && schedule.interval_hours !== 0) next.interval_hours = schedule.interval_hours;
    if (schedule.cron_expr !== undefined && schedule.cron_expr !== "") next.cron_expr = schedule.cron_expr;
    if (schedule.run_at !== undefined) next.run_at = schedule.run_at;
    setForm(next);
  }, [open, policyId, schedule]);

  const errors = showErrors ? validateSchedule(form) : [];
  const busy = create.isPending || update.isPending;

  function patch(next: Partial<ScheduleInput>): void {
    setForm((prev) => ({ ...prev, ...next }));
  }

  function submit(): void {
    setShowErrors(true);
    if (validateSchedule(form).length > 0) return;
    const input: ScheduleInput = { ...form };
    // Strip fields irrelevant to the kind so the server sees a clean shape.
    if (input.kind !== "one_time") delete input.run_at;
    if (input.kind !== "hourly") delete input.interval_hours;
    if (input.kind !== "daily" && input.kind !== "weekly") delete input.time_of_day;
    if (input.kind !== "weekly") delete input.weekday;
    if (input.kind !== "cron") delete input.cron_expr;
    if (schedule === undefined) {
      create.mutate(input);
    } else {
      update.mutate({ scheduleId: schedule.id, input });
    }
  }

  return (
    <Dialog
      open={open}
      title={schedule === undefined ? "Create schedule" : "Edit schedule"}
      description="Pause state is preserved; next-run is recomputed on save."
      onClose={onClose}
    >
      <div className="flex flex-col gap-3">
        <div>
          <label className="mb-1 block text-sm font-medium">Policy</label>
          <Select
            value={form.policy_id}
            onChange={(e) => patch({ policy_id: e.target.value })}
            disabled={schedule !== undefined}
          >
            <option value="">Select…</option>
            {policies.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </Select>
        </div>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div>
            <label className="mb-1 block text-sm font-medium">Kind</label>
            <Select value={form.kind} onChange={(e) => patch({ kind: e.target.value as ScheduleKind })}>
              {SCHEDULE_KINDS.map((k) => (
                <option key={k} value={k}>
                  {SCHEDULE_KIND_LABEL[k]}
                </option>
              ))}
            </Select>
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium">Timezone (IANA)</label>
            <Input value={form.timezone} onChange={(e) => patch({ timezone: e.target.value })} placeholder="UTC" />
          </div>
        </div>

        {form.kind === "one_time" && (
          <div>
            <label className="mb-1 block text-sm font-medium">Run at</label>
            <Input
              type="datetime-local"
              value={toLocalInput(form.run_at)}
              onChange={(e) => patch({ run_at: fromLocalInput(e.target.value) })}
            />
          </div>
        )}
        {form.kind === "hourly" && (
          <div>
            <label className="mb-1 block text-sm font-medium">Every N hours</label>
            <Input
              inputMode="numeric"
              value={form.interval_hours === undefined ? "" : String(form.interval_hours)}
              onChange={(e) => patch({ interval_hours: e.target.value === "" ? undefined : Number(e.target.value) })}
              placeholder="6"
            />
          </div>
        )}
        {(form.kind === "daily" || form.kind === "weekly") && (
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div>
              <label className="mb-1 block text-sm font-medium">Time of day (HH:MM)</label>
              <Input
                value={form.time_of_day ?? ""}
                onChange={(e) => patch({ time_of_day: e.target.value })}
                placeholder="02:00"
                className="font-mono"
              />
            </div>
            {form.kind === "weekly" && (
              <div>
                <label className="mb-1 block text-sm font-medium">Weekday</label>
                <Select
                  value={form.weekday === undefined ? "" : String(form.weekday)}
                  onChange={(e) => patch({ weekday: e.target.value === "" ? undefined : Number(e.target.value) })}
                >
                  <option value="">Select…</option>
                  {WEEKDAYS.map((day, i) => (
                    <option key={day} value={String(i)}>
                      {day}
                    </option>
                  ))}
                </Select>
              </div>
            )}
          </div>
        )}
        {form.kind === "cron" && (
          <div>
            <label className="mb-1 block text-sm font-medium">Cron expression</label>
            <Input
              value={form.cron_expr ?? ""}
              onChange={(e) => patch({ cron_expr: e.target.value })}
              placeholder="0 2 * * *"
              className="font-mono"
            />
          </div>
        )}

        <div>
          <label className="mb-1 block text-sm font-medium">Max concurrency (0 = unlimited)</label>
          <Input
            inputMode="numeric"
            value={form.max_concurrency === undefined ? "" : String(form.max_concurrency)}
            onChange={(e) => patch({ max_concurrency: e.target.value === "" ? undefined : Number(e.target.value) })}
          />
        </div>

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
            {busy ? "Saving…" : schedule === undefined ? "Create schedule" : "Save schedule"}
          </Button>
        </div>
      </div>
    </Dialog>
  );
}

function toLocalInput(iso: string | undefined): string {
  if (iso === undefined || iso === "") return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const pad = (n: number): string => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function fromLocalInput(local: string): string | undefined {
  if (local === "") return undefined;
  const d = new Date(local);
  if (Number.isNaN(d.getTime())) return undefined;
  return d.toISOString();
}

function cadenceOf(s: Schedule): string {
  switch (s.kind) {
    case "one_time":
      return `once at ${formatDateTime(s.run_at)}`;
    case "hourly":
      return `every ${s.interval_hours ?? "?"}h`;
    case "daily":
      return `daily at ${s.time_of_day ?? "?"}`;
    case "weekly":
      return `weekly ${WEEKDAYS[s.weekday ?? 0] ?? ""} at ${s.time_of_day ?? "?"}`;
    case "cron":
      return s.cron_expr ?? "cron";
    default:
      return s.kind;
  }
}

export function SchedulesPage() {
  const [policyId, setPolicyId] = useState<string>("");
  const [dialog, setDialog] = useState<{ open: boolean; schedule?: Schedule }>({ open: false });
  const [runTarget, setRunTarget] = useState<Schedule | undefined>(undefined);
  const [runAgent, setRunAgent] = useState("");
  const [confirmDelete, setConfirmDelete] = useState<Schedule | undefined>(undefined);

  const policies = usePolicies({ limit: 200 });
  const schedules = useSchedules(policyId === "" ? undefined : policyId);
  const pauseResume = usePauseResumeSchedule();
  const run = useRunSchedule();
  const remove = useDeleteSchedule();

  const policyName = (id: ID): string =>
    policies.data?.items.find((p) => p.id === id)?.name ?? id;

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Schedules"
        description="Manage one-time and recurring backup schedules."
        action={
          <Button type="button" onClick={() => setDialog({ open: true })}>
            <Plus aria-hidden />
            Create schedule
          </Button>
        }
      />
      <div className="flex flex-wrap items-center gap-3">
        <Select
          aria-label="Filter by policy"
          className="max-w-sm"
          value={policyId}
          onChange={(e) => setPolicyId(e.target.value)}
        >
          <option value="">All policies</option>
          {(policies.data?.items ?? []).map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </Select>
        {policies.isLoading && <span className="text-sm text-gray-500">Loading policies…</span>}
      </div>

      <Card>
        <CardHeader>
          <CardTitle>All schedules</CardTitle>
        </CardHeader>
        <CardContent>
          {schedules.isLoading ? (
            <TableSkeleton rows={4} cols={5} />
          ) : schedules.data === undefined ? (
            <p className="text-sm text-destructive">Could not load schedules: {errorText(schedules.error)}</p>
          ) : schedules.data.items.length === 0 ? (
            <p className="text-sm text-gray-500">No schedules yet.</p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm">
                <thead>
                  <tr className="border-b border-border text-xs text-gray-500">
                    <th className="py-2 pr-4 font-medium">Policy</th>
                    <th className="py-2 pr-4 font-medium">Cadence</th>
                    <th className="py-2 pr-4 font-medium">Timezone</th>
                    <th className="py-2 pr-4 font-medium">Next run</th>
                    <th className="py-2 pr-4 font-medium">Concurrency</th>
                    <th className="py-2 pr-4 font-medium">State</th>
                    <th className="py-2 pr-4 font-medium">
                      <span className="sr-only">Actions</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {schedules.data.items.map((s) => (
                    <tr key={s.id} className="border-b border-border">
                      <td className="py-2 pr-4 font-medium">{policyName(s.policy_id)}</td>
                      <td className="py-2 pr-4">
                        <span className="font-medium">{SCHEDULE_KIND_LABEL[s.kind as ScheduleKind] ?? s.kind}</span>{" "}
                        <span className="font-mono text-xs text-gray-500">{cadenceOf(s)}</span>
                      </td>
                      <td className="py-2 pr-4 font-mono text-xs">{s.timezone}</td>
                      <td className="py-2 pr-4">{formatDateTime(s.next_run_at)}</td>
                      <td className="py-2 pr-4">{s.max_concurrency === 0 ? "unlimited" : s.max_concurrency}</td>
                      <td className="py-2 pr-4">
                        <span
                          className={
                            s.paused
                              ? "inline-flex items-center rounded-full border border-border bg-surface px-2.5 py-0.5 text-xs font-medium text-gray-500"
                              : "inline-flex items-center rounded-full border border-success/30 bg-success/10 px-2.5 py-0.5 text-xs font-medium text-success"
                          }
                        >
                          {s.paused ? "Paused" : "Active"}
                        </span>
                      </td>
                      <td className="py-2 pr-4">
                        <span className="flex flex-wrap gap-1">
                          <Button type="button" variant="ghost" size="sm" onClick={() => setDialog({ open: true, schedule: s })}>
                            Edit
                          </Button>
                          <Button
                            type="button"
                            variant="ghost"
                            size="sm"
                            disabled={pauseResume.isPending}
                            onClick={() => pauseResume.mutate({ scheduleId: s.id, paused: !s.paused })}
                          >
                            {s.paused ? "Resume" : "Pause"}
                          </Button>
                          <Button type="button" variant="ghost" size="sm" onClick={() => { setRunTarget(s); setRunAgent(""); }}>
                            Run now
                          </Button>
                          <Button type="button" variant="ghost" size="sm" onClick={() => setConfirmDelete(s)}>
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

      <ScheduleDialog
        open={dialog.open}
        policyId={policyId}
        policies={(policies.data?.items ?? []).map((p) => ({ id: p.id, name: p.name }))}
        schedule={dialog.schedule}
        onClose={() => setDialog({ open: false })}
      />

      <Dialog
        open={runTarget !== undefined}
        title="Run schedule now"
        description="Starts a manual run. Leave the agent blank to fan out to every assigned agent."
        onClose={() => setRunTarget(undefined)}
      >
        <div className="flex flex-col gap-4">
          <div>
            <label className="mb-1 block text-sm font-medium">Agent ID (optional)</label>
            <Input
              value={runAgent}
              onChange={(e) => setRunAgent(e.target.value)}
              placeholder="(all assigned agents)"
              className="font-mono"
            />
          </div>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={() => setRunTarget(undefined)}>
              Cancel
            </Button>
            <Button
              type="button"
              disabled={run.isPending || runTarget === undefined}
              onClick={() => {
                if (runTarget === undefined) return;
                run.mutate(
                  { scheduleId: runTarget.id, agentId: runAgent.trim() === "" ? undefined : runAgent.trim() },
                  { onSuccess: () => setRunTarget(undefined) },
                );
              }}
            >
              {run.isPending ? "Starting…" : "Run now"}
            </Button>
          </div>
        </div>
      </Dialog>

      <ConfirmDialog
        open={confirmDelete !== undefined}
        title="Delete schedule?"
        body="Future fires stop. Jobs already created are unaffected."
        confirmLabel="Delete schedule"
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
