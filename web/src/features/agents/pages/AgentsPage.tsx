import { useState } from "react";
import { Check, Copy, Plus } from "lucide-react";
import { Link, useNavigate } from "react-router-dom";
import { errorText } from "@/shared/lib/api";
import { formatBytes, formatDateTime, timeAgo } from "@/shared/lib/format";
import { useDebouncedValue } from "@/shared/hooks/use-debounce";
import { PresenceBadge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/shared/ui/card";
import { ConfirmDialog } from "@/shared/ui/confirm-dialog";
import { Dialog } from "@/shared/ui/dialog";
import { Input } from "@/shared/ui/input";
import { PageHeader } from "@/shared/ui/page-header";
import { Pagination } from "@/shared/ui/pagination";
import { Select } from "@/shared/ui/select";
import { TableSkeleton } from "@/shared/ui/skeleton";
import { useToasts } from "@/shared/ui/toast";
import { useAgents, useArtifacts, useBatchRequestUpdate, useCheckUpdates, useDeleteArtifact, useIssueEnrollToken } from "../hooks";
import type { ID } from "@/shared/types";
import type { Agent } from "../types";

const PAGE_SIZE = 25;

export function StatusCell({ agent }: { agent: Agent }) {
  if (agent.status === "revoked") {
    return (
      <span className="inline-flex items-center rounded-full border border-border bg-surface px-2.5 py-0.5 text-xs font-medium text-gray-500">
        Revoked
      </span>
    );
  }
  return <PresenceBadge presence={agent.status} />;
}

function EnrollTokenDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [ttl, setTtl] = useState("24");
  const [copied, setCopied] = useState(false);
  const issue = useIssueEnrollToken();
  const { push } = useToasts();

  function submit(): void {
    setCopied(false);
    const hours = ttl.trim() === "" ? undefined : Number(ttl);
    if (hours !== undefined && (!Number.isInteger(hours) || hours < 0 || hours > 168)) {
      push({ tone: "error", title: "TTL must be 0–168 hours (blank = 24h default)" });
      return;
    }
    issue.mutate(hours === 0 ? undefined : hours, {
      onError: (err) => push({ tone: "error", title: "Could not issue token", message: errorText(err) }),
    });
  }

  function copy(): void {
    const token = issue.data?.token;
    if (token === undefined) return;
    void navigator.clipboard
      .writeText(token)
      .then(() => setCopied(true))
      .catch(() => push({ tone: "error", title: "Copy failed — select the token manually" }));
  }

  function close(): void {
    issue.reset();
    setCopied(false);
    onClose();
  }

  return (
    <Dialog
      open={open}
      title="Enroll a new agent"
      description="The token is shown once. Paste it into the agent's enroll call, then close this dialog."
      onClose={close}
    >
      {issue.data === undefined ? (
        <div className="flex flex-col gap-4">
          <div>
            <label htmlFor="enroll-ttl" className="mb-1 block text-sm font-medium">
              Token lifetime (hours, max 168)
            </label>
            <Input
              id="enroll-ttl"
              inputMode="numeric"
              value={ttl}
              onChange={(e) => setTtl(e.target.value)}
              placeholder="24"
            />
          </div>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={close}>
              Cancel
            </Button>
            <Button type="button" onClick={submit} disabled={issue.isPending}>
              {issue.isPending ? "Issuing…" : "Issue token"}
            </Button>
          </div>
        </div>
      ) : (
        <div className="flex flex-col gap-3">
          <p className="text-sm font-medium text-destructive">
            Copy this token now — it will never be shown again.
          </p>
          <div className="flex items-center gap-2 rounded-md border border-border bg-surface p-2">
            <code className="min-w-0 flex-1 break-all font-mono text-xs">{issue.data.token}</code>
            <Button type="button" variant="outline" size="sm" onClick={copy}>
              {copied ? <Check aria-hidden /> : <Copy aria-hidden />}
              {copied ? "Copied" : "Copy"}
            </Button>
          </div>
          <p className="text-xs text-gray-500">
            Expires {formatDateTime(issue.data.expires_at)} · credential{" "}
            <span className="font-mono">{issue.data.credential_id}</span>
          </p>
          <div className="flex justify-end">
            <Button type="button" onClick={close}>
              Done
            </Button>
          </div>
        </div>
      )}
    </Dialog>
  );
}

function ArtifactManager() {
  const artifacts = useArtifacts();
  const remove = useDeleteArtifact();
  const [confirmDelete, setConfirmDelete] = useState<string | undefined>(undefined);
  const check = useCheckUpdates();

  return (
    <Card>
      <CardHeader>
        <div className="flex items-start justify-between gap-4">
          <CardTitle>Agent updates</CardTitle>
          <Button type="button" variant="outline" size="sm" onClick={() => check.mutate()} disabled={check.isPending}>
            {check.isPending ? "Checking…" : "Check for updates"}
          </Button>
        </div>
      </CardHeader>
      <CardContent>
        <div className="flex flex-col gap-4">
          <p className="text-sm text-gray-500">
            The manager syncs agent binaries from releases automatically. Cached binaries below can be
            offered to outdated agents from their detail page.
          </p>
          {artifacts.isLoading ? (
            <TableSkeleton rows={3} cols={4} />
          ) : artifacts.data === undefined ? (
            <p className="text-sm text-destructive">Could not load artifacts: {errorText(artifacts.error)}</p>
          ) : artifacts.data.items.length === 0 ? (
            <p className="text-sm text-gray-500">
              No binaries cached yet. Use “Check for updates” to sync the latest release.
            </p>
          ) : (
            <ul className="flex flex-col gap-2">
              {artifacts.data.items.map((a) => (
                <li
                  key={a.id}
                  className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-border p-2 text-sm"
                >
                  <span className="flex flex-wrap items-center gap-2">
                    <span className="font-medium">{a.version}</span>
                    <span className="text-xs text-gray-500">
                      {a.goos}/{a.goarch} · {formatBytes(a.size_bytes)}
                    </span>
                    <code className="font-mono text-xs text-gray-500" title={`sha256:${a.sha256}`}>
                      {a.sha256.slice(0, 12)}…
                    </code>
                  </span>
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    onClick={() => setConfirmDelete(a.id)}
                  >
                    Delete
                  </Button>
                </li>
              ))}
            </ul>
          )}
        </div>
      </CardContent>
      <ConfirmDialog
        open={confirmDelete !== undefined}
        title="Delete artifact?"
        body="Agents with a pending update to this binary will fail with an actionable error instead of updating."
        confirmLabel="Delete artifact"
        busy={remove.isPending}
        onConfirm={() => {
          if (confirmDelete !== undefined) {
            remove.mutate(confirmDelete, { onSuccess: () => setConfirmDelete(undefined) });
          }
        }}
        onClose={() => setConfirmDelete(undefined)}
      />
    </Card>
  );
}

export function AgentsPage() {
  const [search, setSearch] = useState("");
  const [status, setStatus] = useState("");
  const [offset, setOffset] = useState(0);
  const [enrollOpen, setEnrollOpen] = useState(false);
  const [selected, setSelected] = useState<ID[]>([]);
  const [confirmBatch, setConfirmBatch] = useState(false);
  const navigate = useNavigate();
  const q = useDebouncedValue(search.trim());

  const agents = useAgents({
    status: status === "" ? undefined : status,
    q: q === "" ? undefined : q,
    limit: PAGE_SIZE,
    offset,
  });
  const batch = useBatchRequestUpdate(() => {
    setSelected([]);
    setConfirmBatch(false);
  });

  const pageIds = (agents.data?.items ?? []).map((a) => a.id);
  const pageSelected = pageIds.filter((id) => selected.includes(id));
  const allPageSelected = pageIds.length > 0 && pageSelected.length === pageIds.length;

  function toggleOne(id: ID): void {
    setSelected((prev) => (prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id]));
  }

  function togglePage(): void {
    setSelected((prev) =>
      allPageSelected ? prev.filter((id) => !pageIds.includes(id)) : [...prev, ...pageIds.filter((id) => !prev.includes(id))],
    );
  }

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Agents"
        description="Register, inspect, disable, and monitor agents."
        action={
          <Button type="button" onClick={() => setEnrollOpen(true)}>
            <Plus aria-hidden />
            Enroll agent
          </Button>
        }
      />
      <div className="flex flex-wrap items-center gap-3">
        <Input
          type="search"
          placeholder="Search by hostname or IP…"
          aria-label="Search agents"
          className="max-w-sm"
          value={search}
          onChange={(e) => {
            setSearch(e.target.value);
            setOffset(0);
          }}
        />
        <Select
          aria-label="Filter by status"
          className="w-auto"
          value={status}
          onChange={(e) => {
            setStatus(e.target.value);
            setOffset(0);
          }}
        >
          <option value="">All statuses</option>
          <option value="online">Online</option>
          <option value="stale">Stale</option>
          <option value="offline">Offline</option>
          <option value="revoked">Revoked</option>
        </Select>
        {selected.length > 0 && (
          <Button type="button" variant="outline" size="sm" onClick={() => setConfirmBatch(true)}>
            Update selected ({selected.length})
          </Button>
        )}
      </div>
      <Card>
        <CardHeader>
          <CardTitle>All agents</CardTitle>
        </CardHeader>
        <CardContent>
          {agents.isLoading ? (
            <TableSkeleton rows={6} cols={5} />
          ) : agents.data === undefined ? (
            <p className="text-sm text-destructive">Could not load agents: {errorText(agents.error)}</p>
          ) : agents.data.items.length === 0 ? (
            <p className="text-sm text-gray-500">No agents found. Enroll your first agent to get started.</p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm">
                <thead>
                  <tr className="border-b border-border text-xs text-gray-500">
                    <th className="py-2 pr-4">
                      <input
                        type="checkbox"
                        aria-label="Select all agents on this page"
                        checked={allPageSelected}
                        ref={(el) => {
                          if (el !== null) {
                            el.indeterminate = !allPageSelected && pageSelected.length > 0;
                          }
                        }}
                        onChange={togglePage}
                      />
                    </th>
                    <th className="py-2 pr-4 font-medium">Status</th>
                    <th className="py-2 pr-4 font-medium">Hostname</th>
                    <th className="py-2 pr-4 font-medium">IP</th>
                    <th className="py-2 pr-4 font-medium">OS/arch</th>
                    <th className="py-2 pr-4 font-medium">Version</th>
                    <th className="py-2 pr-4 font-medium">Heartbeat</th>
                    <th className="py-2 pr-4 font-medium">Disk free</th>
                  </tr>
                </thead>
                <tbody>
                  {agents.data.items.map((agent) => (
                    <tr
                      key={agent.id}
                      className="cursor-pointer border-b border-border hover:bg-surface"
                      onClick={() => navigate(`/agents/${encodeURIComponent(agent.id)}`)}
                    >
                      <td className="py-2 pr-4" onClick={(e) => e.stopPropagation()}>
                        <input
                          type="checkbox"
                          aria-label={`Select ${agent.hostname}`}
                          checked={selected.includes(agent.id)}
                          onChange={() => toggleOne(agent.id)}
                        />
                      </td>
                      <td className="py-2 pr-4">
                        <span className="flex items-center gap-1">
                          <StatusCell agent={agent} />
                          {agent.outdated && (
                            <Link
                              className="text-xs font-medium text-warning hover:underline"
                              title="Agent version is outdated — open to update"
                              to={`/agents/${encodeURIComponent(agent.id)}`}
                              onClick={(e) => e.stopPropagation()}
                            >
                              outdated
                            </Link>
                          )}
                        </span>
                      </td>
                      <td className="py-2 pr-4 font-medium">{agent.hostname}</td>
                      <td className="py-2 pr-4 font-mono text-xs">{agent.ip_address ?? "—"}</td>
                      <td className="py-2 pr-4">
                        {[agent.os, agent.architecture].filter((s) => s !== undefined && s !== "").join(" / ") || "—"}
                      </td>
                      <td className="py-2 pr-4 font-mono text-xs">{agent.version ?? "—"}</td>
                      <td className="py-2 pr-4" title={formatDateTime(agent.last_heartbeat_at)}>
                        {timeAgo(agent.last_heartbeat_at)}
                      </td>
                      <td className="py-2 pr-4">{formatBytes(agent.disk_free_bytes)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          {agents.data !== undefined && agents.data.total > 0 && (
            <Pagination total={agents.data.total} limit={PAGE_SIZE} offset={offset} onChange={setOffset} />
          )}
        </CardContent>
      </Card>

      <ArtifactManager />

      <ConfirmDialog
        open={confirmBatch}
        title={`Update ${selected.length} agent${selected.length === 1 ? "" : "s"}?`}
        body="Each selected agent is targeted at its latest cached binary and restarts itself once idle. Agents already up to date are skipped."
        confirmLabel="Update selected"
        busy={batch.isPending}
        onConfirm={() => {
          batch.mutate(selected);
        }}
        onClose={() => setConfirmBatch(false)}
      />

      <EnrollTokenDialog open={enrollOpen} onClose={() => setEnrollOpen(false)} />
    </div>
  );
}
