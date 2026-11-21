import { useState } from "react";
import { Play } from "lucide-react";
import type { ID, JobState } from "@/shared/types";
import { errorText } from "@/shared/lib/api";
import { formatBytes, formatDateTime, formatPercent } from "@/shared/lib/format";
import { StateBadge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/shared/ui/card";
import { ConfirmDialog } from "@/shared/ui/confirm-dialog";
import { Dialog } from "@/shared/ui/dialog";
import { Input } from "@/shared/ui/input";
import { PageHeader } from "@/shared/ui/page-header";
import { Pagination } from "@/shared/ui/pagination";
import { Progress } from "@/shared/ui/progress";
import { Select } from "@/shared/ui/select";
import { TableSkeleton } from "@/shared/ui/skeleton";
import {
  useCancelJob,
  useCreateManualJob,
  useJob,
  useJobs,
  useRetryJob,
} from "../hooks";
import { encryptionLabel, isActiveState } from "../types";
import type { Job } from "../types";

const PAGE_SIZE = 25;

const ALL_STATES: JobState[] = [
  "queued",
  "assigned",
  "running",
  "uploading",
  "verifying",
  "succeeded",
  "failed",
  "cancelled",
];

function ManualRunDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [agentId, setAgentId] = useState("");
  const [policyId, setPolicyId] = useState("");
  const [showErrors, setShowErrors] = useState(false);
  const create = useCreateManualJob(onClose);

  function submit(): void {
    setShowErrors(true);
    if (agentId.trim() === "" || policyId.trim() === "") return;
    create.mutate({ agentId: agentId.trim(), policyId: policyId.trim() });
  }

  return (
    <Dialog open={open} title="Run manual backup" description="Queues one job for the agent/policy pair." onClose={onClose}>
      <div className="flex flex-col gap-3">
        <div>
          <label className="mb-1 block text-sm font-medium">Agent ID</label>
          <Input
            value={agentId}
            onChange={(e) => setAgentId(e.target.value)}
            placeholder="agent id"
            className="font-mono"
          />
          {showErrors && agentId.trim() === "" && <p className="mt-1 text-xs text-destructive">Agent ID is required.</p>}
        </div>
        <div>
          <label className="mb-1 block text-sm font-medium">Policy ID</label>
          <Input
            value={policyId}
            onChange={(e) => setPolicyId(e.target.value)}
            placeholder="policy id"
            className="font-mono"
          />
          {showErrors && policyId.trim() === "" && <p className="mt-1 text-xs text-destructive">Policy ID is required.</p>}
        </div>
        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" onClick={onClose} disabled={create.isPending}>
            Cancel
          </Button>
          <Button type="button" onClick={submit} disabled={create.isPending}>
            {create.isPending ? "Queuing…" : "Queue backup"}
          </Button>
        </div>
      </div>
    </Dialog>
  );
}

function JobDetail({ jobId, onClose }: { jobId: ID | undefined; onClose: () => void }) {
  const detail = useJob(jobId);
  const retry = useRetryJob();
  const cancel = useCancelJob();
  const [confirm, setConfirm] = useState<"retry" | "cancel" | null>(null);

  const job = detail.data;
  const active = job !== undefined && isActiveState(job.state);
  const retryable = job !== undefined && (job.state === "failed" || job.state === "cancelled");
  const cancellable = job !== undefined && active;

  return (
    <Dialog open={jobId !== undefined} title="Job detail" description={jobId} wide onClose={onClose}>
      <div>
        {job === undefined ? (
          <p className="text-sm text-gray-500">{detail.isLoading ? "Loading job…" : "Could not load this job."}</p>
        ) : (
          <div className="flex flex-col gap-4">
            <div className="flex flex-wrap items-center gap-2">
              <StateBadge state={job.state} />
              <span className="text-xs text-gray-500">attempt {job.attempt}</span>
              <span className="text-xs text-gray-500">trigger: {job.trigger}</span>
              {active && <span className="text-xs text-gray-500">· live</span>}
            </div>

            <div>
              <div className="mb-1 flex justify-between text-xs text-gray-500">
                <span>Progress</span>
                <span>{job.progress === undefined || job.progress === null ? "—" : formatPercent(job.progress)}</span>
              </div>
              <Progress value={job.progress} />
            </div>

            <dl className="grid grid-cols-1 gap-x-6 gap-y-2 text-sm sm:grid-cols-2">
              <div className="flex justify-between gap-2">
                <dt className="text-gray-500">Agent</dt>
                <dd className="font-mono text-xs">{job.agent_id}</dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="text-gray-500">Policy</dt>
                <dd className="font-mono text-xs">
                  {job.policy_id} <span className="text-gray-500">v{job.policy_version}</span>
                </dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="text-gray-500">Bytes read</dt>
                <dd>{formatBytes(job.bytes_read)}</dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="text-gray-500">Bytes compressed</dt>
                <dd>{formatBytes(job.bytes_compressed)}</dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="text-gray-500">Bytes uploaded</dt>
                <dd>{formatBytes(job.bytes_uploaded)}</dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="text-gray-500">Compression ratio</dt>
                <dd>
                  {job.compression_ratio === undefined || job.compression_ratio === null
                    ? "—"
                    : `${job.compression_ratio.toFixed(2)}×`}
                </dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="text-gray-500">Encryption</dt>
                <dd className="text-xs">{encryptionLabel(job)}</dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="text-gray-500">Destination</dt>
                <dd className="font-mono text-xs">{job.storage_id ?? "—"}</dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="text-gray-500">Snapshot</dt>
                <dd className="font-mono text-xs">{job.snapshot_id ?? "—"}</dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="text-gray-500">Started / finished</dt>
                <dd className="text-xs">
                  {formatDateTime(job.started_at)} → {formatDateTime(job.finished_at)}
                </dd>
              </div>
            </dl>

            {(job.error_code ?? "") !== "" || (job.error_message ?? "") !== "" ? (
              <div className="rounded-md border border-destructive/30 bg-destructive/10 p-3 text-sm">
                <p className="font-medium text-destructive">
                  {job.error_code !== undefined && job.error_code !== "" ? `${job.error_code}: ` : ""}
                  {job.error_message ?? "Job failed."}
                </p>
              </div>
            ) : null}

            {job.retry_history.length > 0 && (
              <div>
                <h3 className="mb-1 text-sm font-semibold">Retry history</h3>
                <ul className="flex flex-col gap-1 text-sm">
                  {job.retry_history.map((a) => (
                    <li key={a.id} className="flex items-center gap-2">
                      <StateBadge state={(a.state as JobState) ?? "queued"} />
                      <span className="font-mono text-xs text-gray-500">
                        {a.id} · attempt {a.attempt}
                      </span>
                    </li>
                  ))}
                </ul>
              </div>
            )}

            <div className="flex flex-wrap gap-2">
              {retryable && (
                <Button type="button" variant="outline" size="sm" onClick={() => setConfirm("retry")}>
                  Retry job
                </Button>
              )}
              {cancellable && (
                <Button type="button" variant="destructive" size="sm" onClick={() => setConfirm("cancel")}>
                  Cancel job
                </Button>
              )}
            </div>
          </div>
        )}
      </div>
      <ConfirmDialog
        open={confirm === "retry"}
        title="Retry job?"
        body="A new attempt is queued with the same policy version. The failed attempt is kept in history."
        confirmLabel="Retry job"
        busy={retry.isPending}
        onConfirm={() => {
          if (jobId !== undefined) retry.mutate(jobId, { onSuccess: () => setConfirm(null) });
        }}
        onClose={() => setConfirm(null)}
      />
      <ConfirmDialog
        open={confirm === "cancel"}
        title="Cancel job?"
        body="The running job is asked to stop. Partial uploads are discarded."
        confirmLabel="Cancel job"
        busy={cancel.isPending}
        onConfirm={() => {
          if (jobId !== undefined) cancel.mutate(jobId, { onSuccess: () => setConfirm(null) });
        }}
        onClose={() => setConfirm(null)}
      />
    </Dialog>
  );
}

function JobRow({ job, selected, onSelect }: { job: Job; selected: boolean; onSelect: () => void }) {
  return (
    <tr
      className={
        selected
          ? "cursor-pointer border-b border-border bg-primary/5"
          : "cursor-pointer border-b border-border hover:bg-surface"
      }
      onClick={onSelect}
    >
      <td className="py-2 pr-4">
        <StateBadge state={job.state} />
      </td>
      <td className="py-2 pr-4 font-mono text-xs" title={job.id}>
        {job.id.slice(0, 8)}…
      </td>
      <td className="py-2 pr-4 font-mono text-xs" title={job.agent_id}>
        {job.agent_id.slice(0, 8)}…
      </td>
      <td className="w-32 py-2 pr-4">
        <Progress value={job.progress} />
      </td>
      <td className="py-2 pr-4 text-xs">{formatBytes(job.bytes_uploaded)}</td>
      <td className="py-2 pr-4 text-xs">{encryptionLabel(job)}</td>
      <td className="py-2 pr-4 text-xs" title={formatDateTime(job.created_at)}>
        {formatDateTime(job.created_at)}
      </td>
    </tr>
  );
}

export function JobsPage() {
  const [state, setState] = useState("");
  const [agentId, setAgentId] = useState("");
  const [policyId, setPolicyId] = useState("");
  const [offset, setOffset] = useState(0);
  const [selected, setSelected] = useState<ID | undefined>(undefined);
  const [manualOpen, setManualOpen] = useState(false);

  const jobs = useJobs({
    state: state === "" ? undefined : state,
    agent_id: agentId.trim() === "" ? undefined : agentId.trim(),
    policy_id: policyId.trim() === "" ? undefined : policyId.trim(),
    limit: PAGE_SIZE,
    offset,
  });

  function resetOffset(): void {
    setOffset(0);
  }

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Jobs"
        description="Monitor, inspect, retry, and cancel backup jobs."
        action={
          <Button type="button" onClick={() => setManualOpen(true)}>
            <Play aria-hidden />
            Run manual backup
          </Button>
        }
      />
      <div className="flex flex-wrap items-center gap-3">
        <Select
          aria-label="Filter by state"
          className="w-auto"
          value={state}
          onChange={(e) => {
            setState(e.target.value);
            resetOffset();
          }}
        >
          <option value="">All states</option>
          {ALL_STATES.map((s) => (
            <option key={s} value={s}>
              {s}
            </option>
          ))}
        </Select>
        <Input
          placeholder="Agent ID…"
          aria-label="Filter by agent ID"
          className="max-w-[12rem] font-mono"
          value={agentId}
          onChange={(e) => {
            setAgentId(e.target.value);
            resetOffset();
          }}
        />
        <Input
          placeholder="Policy ID…"
          aria-label="Filter by policy ID"
          className="max-w-[12rem] font-mono"
          value={policyId}
          onChange={(e) => {
            setPolicyId(e.target.value);
            resetOffset();
          }}
        />
      </div>
      <Card>
        <CardHeader>
          <CardTitle>All jobs</CardTitle>
        </CardHeader>
        <CardContent>
          {jobs.isLoading ? (
            <TableSkeleton rows={6} cols={5} />
          ) : jobs.data === undefined ? (
            <p className="text-sm text-destructive">Could not load jobs: {errorText(jobs.error)}</p>
          ) : jobs.data.items.length === 0 ? (
            <p className="text-sm text-gray-500">No jobs match these filters.</p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm">
                <thead>
                  <tr className="border-b border-border text-xs text-gray-500">
                    <th className="py-2 pr-4 font-medium">State</th>
                    <th className="py-2 pr-4 font-medium">Job</th>
                    <th className="py-2 pr-4 font-medium">Agent</th>
                    <th className="py-2 pr-4 font-medium">Progress</th>
                    <th className="py-2 pr-4 font-medium">Uploaded</th>
                    <th className="py-2 pr-4 font-medium">Encryption</th>
                    <th className="py-2 pr-4 font-medium">Created</th>
                  </tr>
                </thead>
                <tbody>
                  {jobs.data.items.map((job) => (
                    <JobRow
                      key={job.id}
                      job={job}
                      selected={selected === job.id}
                      onSelect={() => setSelected((prev) => (prev === job.id ? undefined : job.id))}
                    />
                  ))}
                </tbody>
              </table>
            </div>
          )}
          {jobs.data !== undefined && jobs.data.total > 0 && (
            <Pagination total={jobs.data.total} limit={PAGE_SIZE} offset={offset} onChange={setOffset} />
          )}
        </CardContent>
      </Card>

      <JobDetail jobId={selected} onClose={() => setSelected(undefined)} />

      <ManualRunDialog open={manualOpen} onClose={() => setManualOpen(false)} />
    </div>
  );
}
