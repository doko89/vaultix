import { useState } from "react";
import { Check, Copy, Plus } from "lucide-react";
import { Link } from "react-router-dom";
import type { ID } from "@/shared/types";
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
import {
  useAgent,
  useAgentPolicies,
  useAgents,
  useIssueEnrollToken,
  useRevokeAgent,
  useSetAgentDisabled,
} from "../hooks";
import type { Agent } from "../types";

const PAGE_SIZE = 25;

function StatusCell({ agent }: { agent: Agent }) {
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

function AgentDetail({ agentId, onClose }: { agentId: ID; onClose: () => void }) {
  const detail = useAgent(agentId);
  const policies = useAgentPolicies(agentId);
  const setDisabled = useSetAgentDisabled();
  const revoke = useRevokeAgent();
  const [confirm, setConfirm] = useState<"disable" | "enable" | "revoke" | null>(null);

  const agent = detail.data;
  const diskUsed =
    agent?.disk_total_bytes !== undefined && agent.disk_free_bytes !== undefined
      ? agent.disk_total_bytes - agent.disk_free_bytes
      : undefined;

  return (
    <Card>
      <CardHeader>
        <div className="flex items-start justify-between gap-4">
          <CardTitle>{agent?.hostname ?? "Agent detail"}</CardTitle>
          <Button type="button" variant="ghost" size="sm" onClick={onClose}>
            Close
          </Button>
        </div>
      </CardHeader>
      <CardContent>
        {agent === undefined ? (
          <p className="text-sm text-gray-500">
            {detail.isLoading ? "Loading agent…" : "Could not load this agent."}
          </p>
        ) : (
          <div className="flex flex-col gap-4">
            <div className="flex flex-wrap items-center gap-2">
              <StatusCell agent={agent} />
              {agent.disabled && (
                <span className="inline-flex items-center rounded-full border border-border bg-surface px-2.5 py-0.5 text-xs font-medium text-gray-500">
                  Disabled
                </span>
              )}
              {agent.outdated && (
                <span className="inline-flex items-center rounded-full border border-warning/30 bg-warning/10 px-2.5 py-0.5 text-xs font-medium text-warning">
                  Version outdated
                </span>
              )}
            </div>
            <dl className="grid grid-cols-1 gap-x-6 gap-y-2 text-sm sm:grid-cols-2">
              <div className="flex justify-between gap-2">
                <dt className="text-gray-500">Hostname</dt>
                <dd className="font-medium">{agent.hostname}</dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="text-gray-500">IP</dt>
                <dd className="font-mono text-xs">{agent.ip_address ?? "—"}</dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="text-gray-500">OS / arch</dt>
                <dd>{[agent.os, agent.architecture].filter((s) => s !== undefined && s !== "").join(" / ") || "—"}</dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="text-gray-500">Version</dt>
                <dd className="font-mono text-xs">{agent.version ?? "—"}</dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="text-gray-500">Last heartbeat</dt>
                <dd title={formatDateTime(agent.last_heartbeat_at)}>{timeAgo(agent.last_heartbeat_at)}</dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="text-gray-500">Disk</dt>
                <dd>
                  {diskUsed === undefined || agent.disk_total_bytes === undefined
                    ? "—"
                    : `${formatBytes(diskUsed)} used of ${formatBytes(agent.disk_total_bytes)}`}
                </dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="text-gray-500">Enrolled</dt>
                <dd>{formatDateTime(agent.enrolled_at)}</dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="text-gray-500">Capabilities</dt>
                <dd className="max-w-[15rem] truncate" title={(agent.capabilities ?? []).join(", ")}>
                  {(agent.capabilities ?? []).join(", ") || "—"}
                </dd>
              </div>
            </dl>
            <div>
              <h3 className="mb-1 text-sm font-semibold">Assigned policies</h3>
              {policies.data === undefined ? (
                <p className="text-sm text-gray-500">
                  {policies.isLoading ? "Loading…" : "Could not load policies."}
                </p>
              ) : policies.data.policies.length === 0 ? (
                <p className="text-sm text-gray-500">No policies target this agent.</p>
              ) : (
                <ul className="flex flex-col gap-1 text-sm">
                  {policies.data.policies.map((p) => (
                    <li key={p.id} className="flex items-center justify-between gap-2">
                      <span>{p.name}</span>
                      <span className="text-xs text-gray-500">
                        v{p.current_version} · {p.enabled ? "enabled" : "disabled"}
                      </span>
                    </li>
                  ))}
                </ul>
              )}
            </div>
            <div className="flex flex-wrap gap-2">
              <Button type="button" variant="outline" size="sm" asChild>
                <Link to={`/agents/${encodeURIComponent(agent.id)}/resources`}>Browse resources</Link>
              </Button>
              {agent.status !== "revoked" &&
                (agent.disabled ? (
                  <Button type="button" variant="outline" size="sm" onClick={() => setConfirm("enable")}>
                    Enable
                  </Button>
                ) : (
                  <Button type="button" variant="outline" size="sm" onClick={() => setConfirm("disable")}>
                    Disable
                  </Button>
                ))}
              {agent.status !== "revoked" && (
                <Button type="button" variant="destructive" size="sm" onClick={() => setConfirm("revoke")}>
                  Revoke
                </Button>
              )}
            </div>
          </div>
        )}
      </CardContent>
      <ConfirmDialog
        open={confirm === "disable"}
        title="Disable agent?"
        body={`Jobs will stop dispatching to ${agent?.hostname ?? "this agent"} until it is re-enabled. Heartbeats keep flowing.`}
        confirmLabel="Disable agent"
        busy={setDisabled.isPending}
        onConfirm={() => {
          setDisabled.mutate({ agentId, disabled: true }, { onSuccess: () => setConfirm(null) });
        }}
        onClose={() => setConfirm(null)}
      />
      <ConfirmDialog
        open={confirm === "enable"}
        title="Enable agent?"
        body={`${agent?.hostname ?? "This agent"} will accept dispatched jobs again.`}
        confirmLabel="Enable agent"
        busy={setDisabled.isPending}
        onConfirm={() => {
          setDisabled.mutate({ agentId, disabled: false }, { onSuccess: () => setConfirm(null) });
        }}
        onClose={() => setConfirm(null)}
      />
      <ConfirmDialog
        open={confirm === "revoke"}
        title="Revoke agent?"
        body="Revocation is irreversible: the agent and its credentials are revoked and it must re-enroll with a new token."
        confirmLabel="Revoke agent"
        requireName={agent?.hostname}
        busy={revoke.isPending}
        onConfirm={() => {
          revoke.mutate(agentId, { onSuccess: () => setConfirm(null) });
        }}
        onClose={() => setConfirm(null)}
      />
    </Card>
  );
}

export function AgentsPage() {
  const [search, setSearch] = useState("");
  const [status, setStatus] = useState("");
  const [offset, setOffset] = useState(0);
  const [selected, setSelected] = useState<ID | undefined>(undefined);
  const [enrollOpen, setEnrollOpen] = useState(false);
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
                      className={
                        selected === agent.id
                          ? "cursor-pointer border-b border-border bg-primary/5"
                          : "cursor-pointer border-b border-border hover:bg-surface"
                      }
                      onClick={() => setSelected((prev) => (prev === agent.id ? undefined : agent.id))}
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

      {selected !== undefined && <AgentDetail agentId={selected} onClose={() => setSelected(undefined)} />}

      <EnrollTokenDialog open={enrollOpen} onClose={() => setEnrollOpen(false)} />
    </div>
  );
}
