import { useState } from "react";
import { Check, Copy, Plus } from "lucide-react";
import { useNavigate } from "react-router-dom";
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
import { useAgents, useArtifacts, useDeleteArtifact, useIssueEnrollToken, useUploadArtifact } from "../hooks";
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
  const [version, setVersion] = useState("");
  const [goos, setGoos] = useState("linux");
  const [goarch, setGoarch] = useState("amd64");
  const [file, setFile] = useState<File | undefined>(undefined);
  const [showErrors, setShowErrors] = useState(false);
  const artifacts = useArtifacts();
  const remove = useDeleteArtifact();
  const [confirmDelete, setConfirmDelete] = useState<string | undefined>(undefined);
  const upload = useUploadArtifact(() => {
    setVersion("");
    setFile(undefined);
    setShowErrors(false);
  });

  function submit(): void {
    setShowErrors(true);
    if (version.trim() === "" || file === undefined) return;
    upload.mutate({ version: version.trim(), goos, goarch, binary: file });
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Agent updates</CardTitle>
      </CardHeader>
      <CardContent>
        <div className="flex flex-col gap-4">
          <div className="flex flex-wrap items-end gap-3">
            <div>
              <label htmlFor="artifact-version" className="mb-1 block text-sm font-medium">
                Version
              </label>
              <Input
                id="artifact-version"
                placeholder="v0.3.0"
                className="w-36"
                value={version}
                onChange={(e) => setVersion(e.target.value)}
              />
              {showErrors && version.trim() === "" && (
                <p className="mt-1 text-xs text-destructive">Version is required.</p>
              )}
            </div>
            <div>
              <label htmlFor="artifact-goos" className="mb-1 block text-sm font-medium">
                OS
              </label>
              <Select
                id="artifact-goos"
                aria-label="Artifact OS"
                className="w-auto"
                value={goos}
                onChange={(e) => setGoos(e.target.value)}
              >
                <option value="linux">linux</option>
                <option value="darwin">darwin</option>
              </Select>
            </div>
            <div>
              <label htmlFor="artifact-goarch" className="mb-1 block text-sm font-medium">
                Arch
              </label>
              <Select
                id="artifact-goarch"
                aria-label="Artifact arch"
                className="w-auto"
                value={goarch}
                onChange={(e) => setGoarch(e.target.value)}
              >
                <option value="amd64">amd64</option>
                <option value="arm64">arm64</option>
              </Select>
            </div>
            <div>
              <label htmlFor="artifact-binary" className="mb-1 block text-sm font-medium">
                Binary
              </label>
              <Input
                id="artifact-binary"
                type="file"
                className="w-auto"
                onChange={(e) => setFile(e.target.files?.[0])}
              />
              {showErrors && file === undefined && (
                <p className="mt-1 text-xs text-destructive">Binary file is required.</p>
              )}
            </div>
            <Button type="button" onClick={submit} disabled={upload.isPending}>
              {upload.isPending ? "Uploading…" : "Upload artifact"}
            </Button>
          </div>
          {artifacts.isLoading ? (
            <TableSkeleton rows={3} cols={4} />
          ) : artifacts.data === undefined ? (
            <p className="text-sm text-destructive">Could not load artifacts: {errorText(artifacts.error)}</p>
          ) : artifacts.data.items.length === 0 ? (
            <p className="text-sm text-gray-500">
              No artifacts hosted. Upload a release binary to offer one-click updates.
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
  const navigate = useNavigate();
  const q = useDebouncedValue(search.trim());

  const agents = useAgents({
    status: status === "" ? undefined : status,
    q: q === "" ? undefined : q,
    limit: PAGE_SIZE,
    offset,
  });

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
                      <td className="py-2 pr-4">
                        <span className="flex items-center gap-1">
                          <StatusCell agent={agent} />
                          {agent.outdated && (
                            <span className="text-xs font-medium text-warning" title="Agent version is outdated">•</span>
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

      <EnrollTokenDialog open={enrollOpen} onClose={() => setEnrollOpen(false)} />
    </div>
  );
}
