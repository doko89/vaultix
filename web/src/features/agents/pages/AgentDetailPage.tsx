import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { formatBytes, formatDateTime, timeAgo } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/shared/ui/card";
import { ConfirmDialog } from "@/shared/ui/confirm-dialog";
import { PageHeader } from "@/shared/ui/page-header";
import {
  useAgent,
  useAgentPolicies,
  useDeleteAgent,
  useRequestUpdate,
  useRevokeAgent,
  useSetAgentDisabled,
  useUpdateDescribe,
} from "../hooks";
import { StatusCell } from "./AgentsPage";

export function AgentDetailPage() {
  const { agentId } = useParams();
  const navigate = useNavigate();
  const detail = useAgent(agentId);
  const policies = useAgentPolicies(agentId);
  const update = useUpdateDescribe(agentId);
  const setDisabled = useSetAgentDisabled();
  const revoke = useRevokeAgent();
  const remove = useDeleteAgent(() => navigate("/agents"));
  const request = useRequestUpdate();
  const [confirm, setConfirm] = useState<"disable" | "enable" | "revoke" | "delete" | "update" | null>(null);

  const agent = detail.data;
  const diskUsed =
    agent?.disk_total_bytes !== undefined && agent.disk_free_bytes !== undefined
      ? agent.disk_total_bytes - agent.disk_free_bytes
      : undefined;

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={agent?.hostname ?? "Agent"}
        description={agentId ?? ""}
        action={
          <Button type="button" variant="outline" asChild>
            <Link to="/agents">Back to agents</Link>
          </Button>
        }
      />
      <Card>
        <CardHeader>
          <CardTitle>Agent detail</CardTitle>
        </CardHeader>
        <CardContent>
          {agentId === undefined ? (
            <p className="text-sm text-destructive">Missing agent id in the URL.</p>
          ) : agent === undefined ? (
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
                        <Link
                          className="text-primary hover:underline"
                          to={`/policies/${encodeURIComponent(p.id)}`}
                        >
                          {p.name}
                        </Link>
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
                {agent.status === "revoked" && (
                  <Button type="button" variant="destructive" size="sm" onClick={() => setConfirm("delete")}>
                    Delete
                  </Button>
                )}
              </div>
              {update.data !== undefined &&
                agent.status !== "revoked" &&
                (update.data.pending?.status === "pending" ? (
                  <p className="text-sm text-gray-500">
                    Update to {update.data.pending.target_version} ({update.data.pending.goos}/
                    {update.data.pending.goarch}) pending — applies when the agent is idle.
                  </p>
                ) : (
                  <div className="flex flex-wrap items-center gap-2">
                    {update.data.pending?.status === "failed" && (
                      <p className="text-sm text-destructive">
                        Update to {update.data.pending.target_version} failed:{" "}
                        {update.data.pending.error !== "" ? update.data.pending.error : "unknown error"}
                      </p>
                    )}
                    {update.data.available && update.data.latest !== null && (
                      <Button type="button" variant="outline" size="sm" onClick={() => setConfirm("update")}>
                        Update to {update.data.latest.version}
                      </Button>
                    )}
                  </div>
                ))}
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
            if (agentId !== undefined) {
              setDisabled.mutate({ agentId, disabled: true }, { onSuccess: () => setConfirm(null) });
            }
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
            if (agentId !== undefined) {
              setDisabled.mutate({ agentId, disabled: false }, { onSuccess: () => setConfirm(null) });
            }
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
            if (agentId !== undefined) {
              revoke.mutate(agentId, { onSuccess: () => setConfirm(null) });
            }
          }}
          onClose={() => setConfirm(null)}
        />
        <ConfirmDialog
          open={confirm === "delete"}
          title="Delete agent?"
          body="The agent row is permanently removed. Job and snapshot history is kept but no longer linked to this agent."
          confirmLabel="Delete agent"
          requireName={agent?.hostname}
          busy={remove.isPending}
          onConfirm={() => {
            if (agentId !== undefined) {
              remove.mutate(agentId);
            }
          }}
          onClose={() => setConfirm(null)}
        />
        <ConfirmDialog
          open={confirm === "update"}
          title={`Update agent to ${update.data?.latest?.version ?? "latest"}?`}
          body="The agent downloads the binary from the manager, verifies its checksum, and restarts itself once idle. A backup of the current binary is kept for manual recovery."
          confirmLabel="Update agent"
          busy={request.isPending}
          onConfirm={() => {
            const latest = update.data?.latest;
            if (agentId !== undefined && latest !== undefined && latest !== null) {
              request.mutate({ agentId, artifactId: latest.id }, { onSuccess: () => setConfirm(null) });
            }
          }}
          onClose={() => setConfirm(null)}
        />
      </Card>
    </div>
  );
}
