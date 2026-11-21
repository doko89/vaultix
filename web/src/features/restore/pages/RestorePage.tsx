import { useMemo, useState } from "react";
import { RotateCcw } from "lucide-react";
import { useSearchParams } from "react-router-dom";
import type { ID } from "@/shared/types";
import { errorText } from "@/shared/lib/api";
import { formatDateTime } from "@/shared/lib/format";
import { StateBadge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/shared/ui/card";
import { Dialog } from "@/shared/ui/dialog";
import { Input } from "@/shared/ui/input";
import { PageHeader } from "@/shared/ui/page-header";
import { Progress } from "@/shared/ui/progress";
import { Select } from "@/shared/ui/select";
import { useToasts } from "@/shared/ui/toast";
import {
  useConfirmRestore,
  useCurrentActor,
  useExecuteRestore,
  usePlanRestore,
  useRestorableSnapshots,
  useRestoreStatus,
} from "../hooks";
import { kindForTargetType, RESTORE_KINDS } from "../types";
import type { RestoreKind, RestorePlan, RestoreSnapshot } from "../types";
import { useAgents } from "@/features/agents/hooks";

// Restore wizard: select snapshot → targets → destination → explicit typed
// confirm → execute → status polling. Overwrite warnings from the plan are
// always shown before confirmation (DESIGN section 7, PRD §7.13).

type Step = "snapshot" | "options" | "plan" | "confirm" | "status";

const STEP_LABEL: Record<Step, string> = {
  snapshot: "1. Snapshot",
  options: "2. Targets",
  plan: "3. Plan",
  confirm: "4. Confirm",
  status: "5. Status",
};

const STEPS: Step[] = ["snapshot", "options", "plan", "confirm", "status"];

export function RestorePage() {
  const [searchParams] = useSearchParams();
  const preselected = searchParams.get("snapshot") ?? undefined;

  const [wizardOpen, setWizardOpen] = useState(false);
  const [step, setStep] = useState<Step>("snapshot");
  const [snapshotId, setSnapshotId] = useState<ID | undefined>(preselected);
  const [kind, setKind] = useState<RestoreKind>("file");
  const [paths, setPaths] = useState("");
  const [alternate, setAlternate] = useState(false);
  const [destination, setDestination] = useState("");
  const [targetDb, setTargetDb] = useState("");
  const [targetAgentId, setTargetAgentId] = useState("");
  const [plan, setPlan] = useState<RestorePlan | undefined>(undefined);
  const [typed, setTyped] = useState("");
  const [jobId, setJobId] = useState<ID | undefined>(undefined);

  const snapshots = useRestorableSnapshots();
  const agents = useAgents({ limit: 100 });
  const actor = useCurrentActor();
  const planMut = usePlanRestore();
  const confirmMut = useConfirmRestore();
  const executeMut = useExecuteRestore();
  const status = useRestoreStatus(jobId);
  const { push } = useToasts();

  const selected: RestoreSnapshot | undefined = useMemo(
    () => snapshots.data?.items.find((s) => s.id === snapshotId),
    [snapshots.data, snapshotId],
  );

  const actorName =
    actor.data?.user.display_name !== "" && actor.data?.user.display_name !== undefined
      ? (actor.data?.user.display_name as string)
      : (actor.data?.user.username ?? "");

  function openWizard(): void {
    setWizardOpen(true);
    setStep("snapshot");
    setPlan(undefined);
    setTyped("");
    setJobId(undefined);
    if (preselected !== undefined) {
      setSnapshotId(preselected);
      const found = snapshots.data?.items.find((s) => s.id === preselected);
      if (found !== undefined) setKind(kindForTargetType(found.target_type));
    }
  }

  function pickSnapshot(id: ID): void {
    setSnapshotId(id);
    const found = snapshots.data?.items.find((s) => s.id === id);
    if (found !== undefined) setKind(kindForTargetType(found.target_type));
  }

  function requestPlan(): void {
    if (selected === undefined || actorName === "") {
      push({ tone: "error", title: "Pick a snapshot first" });
      return;
    }
    const pathList = paths.split(",").map((p) => p.trim()).filter((p) => p !== "");
    planMut.mutate(
      {
        kind,
        snapshot_id: selected.id,
        paths: pathList.length > 0 ? pathList : undefined,
        destination: alternate && destination.trim() !== "" ? destination.trim() : undefined,
        target_db: targetDb.trim() === "" ? undefined : targetDb.trim(),
        target_agent_id: targetAgentId === "" ? undefined : targetAgentId,
        requested_by: actorName,
      },
      {
        onSuccess: (p) => {
          setPlan(p);
          setStep("plan");
        },
        onError: (err) => push({ tone: "error", title: "Could not build plan", message: errorText(err) }),
      },
    );
  }

  function confirmAndExecute(): void {
    if (plan === undefined || actorName === "") return;
    confirmMut.mutate(
      { planId: plan.id, actor: actorName },
      {
        onSuccess: (conf) => {
          executeMut.mutate(
            { token: conf.token, actor: actorName },
            {
              onSuccess: (job) => {
                setJobId(job.id);
                setStep("status");
                push({ tone: "success", title: "Restore started" });
              },
              onError: (err) => push({ tone: "error", title: "Could not execute restore", message: errorText(err) }),
            },
          );
        },
        onError: (err) => push({ tone: "error", title: "Could not confirm plan", message: errorText(err) }),
      },
    );
  }

  const confirmBlocked = selected === undefined || typed !== selected.selector;
  const busyConfirm = confirmMut.isPending || executeMut.isPending;

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Restore"
        description="Create and monitor restore jobs with explicit confirmation."
        action={
          <Button type="button" onClick={openWizard}>
            <RotateCcw aria-hidden />
            Start restore
          </Button>
        }
      />

      <Card>
        <CardHeader>
          <CardTitle>Restore jobs</CardTitle>
        </CardHeader>
        <CardContent>
          {jobId === undefined || status.data === undefined ? (
            <p className="text-sm text-gray-500">
              {status.isLoading
                ? "Loading restore status…"
                : "No restore running in this session. Start a restore to track it here."}
            </p>
          ) : (
            <div className="flex flex-col gap-3">
              <div className="flex flex-wrap items-center gap-2">
                <StateBadge state={status.data.state} />
                <code className="font-mono text-xs">{status.data.id}</code>
                {status.data.approved_by !== undefined && status.data.approved_by !== "" && (
                  <span className="text-xs text-gray-500">approved by {status.data.approved_by}</span>
                )}
              </div>
              <div>
                <div className="mb-1 flex justify-between text-xs text-gray-500">
                  <span>Progress</span>
                  <span>{status.data.progress === undefined ? "—" : `${Math.round(status.data.progress * 100)}%`}</span>
                </div>
                <Progress value={status.data.progress} />
              </div>
              {status.data.target_path !== undefined && status.data.target_path !== "" && (
                <p className="text-sm">
                  Target: <code className="font-mono text-xs">{status.data.target_path}</code>
                </p>
              )}
              {(status.data.error_message ?? "") !== "" && (
                <div className="rounded-md border border-destructive/30 bg-destructive/10 p-3 text-sm">
                  <p className="font-medium text-destructive">
                    {status.data.error_code !== undefined && status.data.error_code !== ""
                      ? `${status.data.error_code}: `
                      : ""}
                    {status.data.error_message}
                  </p>
                </div>
              )}
            </div>
          )}
        </CardContent>
      </Card>

      <Dialog
        open={wizardOpen}
        title="Restore wizard"
        description="Plans are explicit: every write is listed before anything runs."
        onClose={() => setWizardOpen(false)}
        wide
      >
        <ol className="mb-4 flex flex-wrap gap-2" aria-label="Restore steps">
          {STEPS.map((s) => (
            <li
              key={s}
              className={
                s === step
                  ? "rounded-full bg-primary/10 px-3 py-1 text-xs font-medium text-primary"
                  : "rounded-full bg-surface px-3 py-1 text-xs text-gray-500"
              }
            >
              {STEP_LABEL[s]}
            </li>
          ))}
        </ol>

        {step === "snapshot" && (
          <div className="flex flex-col gap-3">
            {snapshots.isLoading ? (
              <p className="text-sm text-gray-500">Loading restorable snapshots…</p>
            ) : snapshots.data === undefined ? (
              <p className="text-sm text-destructive">Could not load snapshots: {errorText(snapshots.error)}</p>
            ) : snapshots.data.items.length === 0 ? (
              <p className="text-sm text-gray-500">No restorable snapshots. Only verified snapshots can be restored.</p>
            ) : (
              <ul className="flex max-h-80 flex-col gap-1 overflow-y-auto">
                {snapshots.data.items.map((s) => (
                  <li key={s.id}>
                    <button
                      type="button"
                      onClick={() => pickSnapshot(s.id)}
                      className={
                        s.id === snapshotId
                          ? "flex w-full items-center justify-between gap-2 rounded-md border border-primary bg-primary/5 px-3 py-2 text-left text-sm"
                          : "flex w-full items-center justify-between gap-2 rounded-md border border-border px-3 py-2 text-left text-sm hover:bg-surface"
                      }
                    >
                      <span className="min-w-0">
                        <span className="block truncate font-mono text-xs">{s.selector}</span>
                        <span className="text-xs text-gray-500">
                          {s.target_type} · {formatDateTime(s.completed_at)} ·{" "}
                          {s.encryption === "encrypted" ? "Encrypted" : "Unencrypted"}
                        </span>
                      </span>
                      {s.id === snapshotId && <span className="text-xs font-medium text-primary">selected</span>}
                    </button>
                  </li>
                ))}
              </ul>
            )}
            <div className="flex justify-end">
              <Button type="button" disabled={selected === undefined} onClick={() => setStep("options")}>
                Continue
              </Button>
            </div>
          </div>
        )}

        {step === "options" && selected !== undefined && (
          <div className="flex flex-col gap-3">
            <div>
              <label className="mb-1 block text-sm font-medium">Restore kind</label>
              <Select value={kind} onChange={(e) => setKind(e.target.value as RestoreKind)}>
                {RESTORE_KINDS.map((k) => (
                  <option key={k} value={k}>
                    {k}
                  </option>
                ))}
              </Select>
            </div>
            <div>
              <label className="mb-1 block text-sm font-medium">Paths subset (optional, comma-separated)</label>
              <Input
                value={paths}
                onChange={(e) => setPaths(e.target.value)}
                placeholder="Empty restores the whole snapshot scope"
                className="font-mono"
              />
              <p className="mt-1 text-xs text-gray-500">Restore kind “stack” is planned per snapshot here; multi-snapshot stack ordering is a server-side plan option.</p>
            </div>
            <div className="rounded-md border border-border p-3">
              <label className="flex items-center gap-2 text-sm font-medium">
                <input type="checkbox" checked={alternate} onChange={(e) => setAlternate(e.target.checked)} />
                Restore to an alternate destination
              </label>
              {alternate ? (
                <Input
                  value={destination}
                  onChange={(e) => setDestination(e.target.value)}
                  placeholder="/restore/target (alternate path)"
                  className="mt-2 font-mono"
                />
              ) : (
                <p className="mt-1 text-xs text-warning">
                  Original location: existing files will be overwritten. The plan lists every write.
                </p>
              )}
            </div>
            {kind === "database" && (
              <div>
                <label className="mb-1 block text-sm font-medium">Target database (optional)</label>
                <Input
                  value={targetDb}
                  onChange={(e) => setTargetDb(e.target.value)}
                  placeholder="Empty restores into the source database"
                  className="font-mono"
                />
              </div>
            )}
            <div>
              <label className="mb-1 block text-sm font-medium">Target agent</label>
              <Select value={targetAgentId} onChange={(e) => setTargetAgentId(e.target.value)}>
                <option value="">Source agent (default)</option>
                {(agents.data?.items ?? [])
                  .filter((a) => a.status !== "revoked")
                  .map((a) => (
                    <option key={a.id} value={a.id}>
                      {a.hostname} ({a.status})
                    </option>
                  ))}
              </Select>
              <p className="mt-1 text-xs text-gray-500">
                Restore to a different agent when the source VM is gone. The plan warns before anything runs.
              </p>
            </div>
            <div className="flex justify-between">
              <Button type="button" variant="outline" onClick={() => setStep("snapshot")}>
                Back
              </Button>
              <Button type="button" onClick={requestPlan} disabled={planMut.isPending}>
                {planMut.isPending ? "Planning…" : "Build plan"}
              </Button>
            </div>
          </div>
        )}

        {step === "plan" && (
          <div className="flex flex-col gap-3">
            {plan === undefined ? (
              <p className="text-sm text-gray-500">No plan yet.</p>
            ) : (
              <>
                <p className="text-sm text-gray-500">
                  Target agent:{" "}
                  <span className="font-medium text-foreground">
                    {(agents.data?.items ?? []).find((a) => a.id === plan.agent_id)?.hostname ?? plan.agent_id}
                  </span>
                </p>
                {plan.warnings.length > 0 && (
                  <div className="rounded-md border border-warning/30 bg-warning/10 p-3">
                    <p className="text-sm font-medium text-warning">Warnings — read before confirming</p>
                    <ul className="mt-1 flex flex-col gap-1 text-sm">
                      {plan.warnings.map((w, i) => (
                        <li key={i}>{w}</li>
                      ))}
                    </ul>
                  </div>
                )}
                {(plan.compatibility ?? []).length > 0 && (
                  <div className="rounded-md border border-info/30 bg-info/10 p-3 text-sm">
                    <p className="font-medium text-info">Compatibility notes</p>
                    <ul className="mt-1 flex flex-col gap-1">
                      {(plan.compatibility ?? []).map((c, i) => (
                        <li key={i}>{c}</li>
                      ))}
                    </ul>
                  </div>
                )}
                <ol className="flex flex-col gap-2">
                  {plan.steps.map((s) => (
                    <li key={s.sequence} className="rounded-md border border-border p-2 text-sm">
                      <p className="font-medium">
                        {s.sequence}. {s.kind}: <code className="font-mono text-xs">{s.source}</code> →{" "}
                        <code className="font-mono text-xs">{s.destination}</code>
                      </p>
                      {(s.notes ?? []).length > 0 && (
                        <ul className="mt-1 text-xs text-gray-500">
                          {(s.notes ?? []).map((n, i) => (
                            <li key={i}>{n}</li>
                          ))}
                        </ul>
                      )}
                    </li>
                  ))}
                </ol>
                <div className="flex justify-between">
                  <Button type="button" variant="outline" onClick={() => setStep("options")}>
                    Back
                  </Button>
                  <Button
                    type="button"
                    onClick={() => {
                      setTyped("");
                      setStep("confirm");
                    }}
                  >
                    Review & confirm
                  </Button>
                </div>
              </>
            )}
          </div>
        )}

        {step === "confirm" && selected !== undefined && (
          <div className="flex flex-col gap-3">
            <p className="text-sm text-gray-500">
              This restores <code className="font-mono text-xs">{selected.selector}</code>
              {alternate && destination.trim() !== "" ? (
                <>
                  {" "}to <code className="font-mono text-xs">{destination.trim()}</code>
                </>
              ) : (
                " in place (original location)"
              )}
              . Type the source selector to confirm.
            </p>
            <div>
              <label htmlFor="restore-confirm" className="mb-1 block text-sm font-medium">
                Type <span className="font-mono">{selected.selector}</span> to confirm
              </label>
              <Input
                id="restore-confirm"
                value={typed}
                onChange={(e) => setTyped(e.target.value)}
                autoComplete="off"
                placeholder={selected.selector}
                className="font-mono"
              />
            </div>
            <div className="flex justify-between">
              <Button type="button" variant="outline" onClick={() => setStep("plan")} disabled={busyConfirm}>
                Back
              </Button>
              <Button type="button" variant="destructive" onClick={confirmAndExecute} disabled={confirmBlocked || busyConfirm}>
                {busyConfirm ? "Starting…" : "Confirm & execute"}
              </Button>
            </div>
          </div>
        )}

        {step === "status" && (
          <div className="flex flex-col gap-3">
            {status.data === undefined ? (
              <p className="text-sm text-gray-500">{status.isLoading ? "Starting restore…" : "Waiting for status…"}</p>
            ) : (
              <div className="flex flex-col gap-2">
                <div className="flex items-center gap-2">
                  <StateBadge state={status.data.state} />
                  <code className="font-mono text-xs">{status.data.id}</code>
                </div>
                <Progress value={status.data.progress} />
                {(status.data.error_message ?? "") !== "" && (
                  <p className="text-sm text-destructive">{status.data.error_message}</p>
                )}
              </div>
            )}
            <div className="flex justify-end">
              <Button type="button" onClick={() => setWizardOpen(false)}>
                Done
              </Button>
            </div>
          </div>
        )}
      </Dialog>
    </div>
  );
}
