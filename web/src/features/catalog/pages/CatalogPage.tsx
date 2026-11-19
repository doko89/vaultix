import { useState } from "react";
import { Link } from "react-router-dom";
import type { ID } from "@/shared/types";
import { errorText } from "@/shared/lib/api";
import { formatBytes, formatDateTime } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { PageHeader } from "@/shared/ui/page-header";
import { Pagination } from "@/shared/ui/pagination";
import { Select } from "@/shared/ui/select";
import { TableSkeleton } from "@/shared/ui/skeleton";
import { useManifest, useSnapshot, useSnapshots } from "../hooks";
import { snapshotEncryptionLabel } from "../types";

const PAGE_SIZE = 25;

const TARGET_TYPES = [
  "host_filesystem",
  "docker_config",
  "docker_volume",
  "postgres",
  "mysql",
  "mssql",
  "redis",
  "app_stack",
] as const;

function SnapshotDetailCard({ snapshotId, onClose }: { snapshotId: ID; onClose: () => void }) {
  const [showManifest, setShowManifest] = useState(false);
  const detail = useSnapshot(snapshotId);
  const manifest = useManifest(snapshotId, showManifest);

  const snapshot = detail.data?.snapshot;
  const embedded = detail.data?.manifest;

  return (
    <Card>
      <CardHeader>
        <div className="flex items-start justify-between gap-4">
          <CardTitle className="font-mono text-base">{snapshotId}</CardTitle>
          <Button type="button" variant="ghost" size="sm" onClick={onClose}>
            Close
          </Button>
        </div>
      </CardHeader>
      <CardContent>
        {snapshot === undefined ? (
          <p className="text-sm text-gray-500">
            {detail.isLoading ? "Loading snapshot…" : "Could not load this snapshot."}
          </p>
        ) : (
          <div className="flex flex-col gap-4">
            <div className="flex flex-wrap items-center gap-2">
              <span className="inline-flex items-center rounded-full border border-border bg-surface px-2.5 py-0.5 text-xs font-medium">
                {snapshot.status}
              </span>
              {snapshot.restorable ? (
                <span className="inline-flex items-center rounded-full border border-success/30 bg-success/10 px-2.5 py-0.5 text-xs font-medium text-success">
                  Restorable
                </span>
              ) : (
                <span className="inline-flex items-center rounded-full border border-warning/30 bg-warning/10 px-2.5 py-0.5 text-xs font-medium text-warning">
                  Not restorable
                </span>
              )}
              <span className="text-xs text-gray-500">{snapshotEncryptionLabel(snapshot)}</span>
            </div>

            <dl className="grid grid-cols-1 gap-x-6 gap-y-2 text-sm sm:grid-cols-2">
              <div className="flex justify-between gap-2">
                <dt className="text-gray-500">Agent</dt>
                <dd className="font-mono text-xs">{snapshot.agent_id}</dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="text-gray-500">Policy</dt>
                <dd className="font-mono text-xs">
                  {snapshot.policy_id} <span className="text-gray-500">v{snapshot.policy_version}</span>
                </dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="text-gray-500">Target</dt>
                <dd className="text-xs">
                  {snapshot.target_type} · <span className="font-mono">{snapshot.selector}</span>
                </dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="text-gray-500">Job</dt>
                <dd className="font-mono text-xs">{snapshot.job_id}</dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="text-gray-500">Size</dt>
                <dd>
                  {formatBytes(snapshot.bytes_original)} → {formatBytes(snapshot.bytes_stored)}
                </dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="text-gray-500">Objects</dt>
                <dd>{snapshot.object_count}</dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="text-gray-500">Completed</dt>
                <dd>{formatDateTime(snapshot.completed_at)}</dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="text-gray-500">Manifest</dt>
                <dd className="font-mono text-xs">{snapshot.manifest_id === "" ? "—" : snapshot.manifest_id}</dd>
              </div>
            </dl>

            <div className="flex flex-wrap gap-2">
              <Button type="button" variant="outline" size="sm" onClick={() => setShowManifest((v) => !v)}>
                {showManifest ? "Hide manifest" : "View manifest"}
              </Button>
              {snapshot.restorable && (
                <Button type="button" variant="outline" size="sm" asChild>
                  <Link to={`/restore?snapshot=${encodeURIComponent(snapshot.id)}`}>Restore…</Link>
                </Button>
              )}
            </div>

            {showManifest && (
              <div className="rounded-md border border-border p-3">
                {manifest.isLoading || embedded === undefined ? (
                  manifest.isLoading ? (
                    <p className="text-sm text-gray-500">Loading manifest…</p>
                  ) : (
                    <SnapshotManifestView snapshotId={snapshotId} />
                  )
                ) : (
                  <ManifestView
                    checksum={embedded.checksum}
                    objects={embedded.objects.map((o) => ({ key: o.object_key, kind: o.kind, size: o.size }))}
                  />
                )}
              </div>
            )}
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function SnapshotManifestView({ snapshotId }: { snapshotId: ID }) {
  const manifest = useManifest(snapshotId, true);
  if (manifest.isLoading) return <p className="text-sm text-gray-500">Loading manifest…</p>;
  if (manifest.data === undefined) {
    return <p className="text-sm text-destructive">Could not load manifest: {errorText(manifest.error)}</p>;
  }
  return (
    <ManifestView
      checksum={manifest.data.checksum}
      objects={manifest.data.objects.map((o) => ({ key: o.object_key, kind: o.kind, size: o.size }))}
    />
  );
}

function ManifestView({ checksum, objects }: { checksum: string; objects: { key: string; kind: string; size: number }[] }) {
  return (
    <div className="flex flex-col gap-2 text-sm">
      <p className="font-mono text-xs text-gray-500">checksum {checksum}</p>
      {objects.length === 0 ? (
        <p className="text-sm text-gray-500">No objects recorded.</p>
      ) : (
        <ul className="flex max-h-60 flex-col gap-1 overflow-y-auto">
          {objects.map((o, i) => (
            <li key={i} className="flex items-center justify-between gap-2 font-mono text-xs">
              <span className="min-w-0 flex-1 truncate">{o.key}</span>
              <span className="text-gray-500">
                {o.kind} · {formatBytes(o.size)}
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

export function CatalogPage() {
  const [agentId, setAgentId] = useState("");
  const [policyId, setPolicyId] = useState("");
  const [target, setTarget] = useState("");
  const [restorableOnly, setRestorableOnly] = useState(false);
  const [offset, setOffset] = useState(0);
  const [selected, setSelected] = useState<ID | undefined>(undefined);

  const snapshots = useSnapshots({
    agent_id: agentId.trim() === "" ? undefined : agentId.trim(),
    policy_id: policyId.trim() === "" ? undefined : policyId.trim(),
    target: target === "" ? undefined : target,
    restorable_only: restorableOnly,
    limit: PAGE_SIZE,
    offset,
  });

  function resetOffset(): void {
    setOffset(0);
  }

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Catalog"
        description="Browse snapshots and manifests by agent, policy, target, and date."
      />
      <div className="flex flex-wrap items-center gap-3">
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
        <Select
          aria-label="Filter by target type"
          className="w-auto"
          value={target}
          onChange={(e) => {
            setTarget(e.target.value);
            resetOffset();
          }}
        >
          <option value="">All targets</option>
          {TARGET_TYPES.map((t) => (
            <option key={t} value={t}>
              {t}
            </option>
          ))}
        </Select>
        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={restorableOnly}
            onChange={(e) => {
              setRestorableOnly(e.target.checked);
              resetOffset();
            }}
          />
          Restorable only
        </label>
      </div>
      <Card>
        <CardHeader>
          <CardTitle>All snapshots</CardTitle>
        </CardHeader>
        <CardContent>
          {snapshots.isLoading ? (
            <TableSkeleton rows={6} cols={5} />
          ) : snapshots.data === undefined ? (
            <p className="text-sm text-destructive">Could not load snapshots: {errorText(snapshots.error)}</p>
          ) : snapshots.data.items.length === 0 ? (
            <p className="text-sm text-gray-500">No snapshots match these filters.</p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm">
                <thead>
                  <tr className="border-b border-border text-xs text-gray-500">
                    <th className="py-2 pr-4 font-medium">Selector</th>
                    <th className="py-2 pr-4 font-medium">Target</th>
                    <th className="py-2 pr-4 font-medium">Status</th>
                    <th className="py-2 pr-4 font-medium">Encryption</th>
                    <th className="py-2 pr-4 font-medium">Size</th>
                    <th className="py-2 pr-4 font-medium">Completed</th>
                  </tr>
                </thead>
                <tbody>
                  {snapshots.data.items.map((s) => (
                    <tr
                      key={s.id}
                      className={
                        selected === s.id
                          ? "cursor-pointer border-b border-border bg-primary/5"
                          : "cursor-pointer border-b border-border hover:bg-surface"
                      }
                      onClick={() => setSelected((prev) => (prev === s.id ? undefined : s.id))}
                    >
                      <td className="py-2 pr-4 font-mono text-xs" title={s.id}>
                        {s.selector}
                      </td>
                      <td className="py-2 pr-4 text-xs">{s.target_type}</td>
                      <td className="py-2 pr-4">
                        {s.restorable ? (
                          <span className="inline-flex items-center rounded-full border border-success/30 bg-success/10 px-2.5 py-0.5 text-xs font-medium text-success">
                            Restorable
                          </span>
                        ) : (
                          <span className="inline-flex items-center rounded-full border border-border bg-surface px-2.5 py-0.5 text-xs font-medium text-gray-500">
                            {s.status}
                          </span>
                        )}
                      </td>
                      <td className="py-2 pr-4 text-xs">{snapshotEncryptionLabel(s)}</td>
                      <td className="py-2 pr-4 text-xs">{formatBytes(s.bytes_stored)}</td>
                      <td className="py-2 pr-4 text-xs">{formatDateTime(s.completed_at)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          {snapshots.data !== undefined && snapshots.data.total > 0 && (
            <Pagination total={snapshots.data.total} limit={PAGE_SIZE} offset={offset} onChange={setOffset} />
          )}
        </CardContent>
      </Card>

      {selected !== undefined && (
        <SnapshotDetailCard snapshotId={selected} onClose={() => setSelected(undefined)} />
      )}
    </div>
  );
}
