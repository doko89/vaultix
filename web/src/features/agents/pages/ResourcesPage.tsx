import { useMemo, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { Folder, Container, HardDrive, Database, Layers, Package } from "lucide-react";
import { errorText } from "@/shared/lib/api";
import { formatDateTime } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { PageHeader } from "@/shared/ui/page-header";
import { Select } from "@/shared/ui/select";
import { TableSkeleton } from "@/shared/ui/skeleton";
import { useAgent, useAgentResources } from "../hooks";
import { groupOfKind, resourceGroupLabel } from "../types";
import type { ResourceGroup } from "../types";

const GROUP_ICON: Record<ResourceGroup, typeof Folder> = {
  host: Folder,
  container: Container,
  volume: HardDrive,
  database: Database,
  stack: Layers,
  other: Package,
};

const KIND_OPTIONS = [
  "host_path",
  "mount",
  "docker_container",
  "docker_volume",
  "docker_bind_mount",
  "compose_project",
  "postgres_database",
  "mysql_database",
  "mssql_database",
  "redis_dataset",
] as const;

export function ResourcesPage() {
  const { agentId } = useParams();
  const [kind, setKind] = useState("");
  const [search, setSearch] = useState("");
  const agent = useAgent(agentId);
  const resources = useAgentResources(agentId, kind === "" ? undefined : kind);

  const filtered = useMemo(() => {
    const items = resources.data?.items ?? [];
    const q = search.trim().toLowerCase();
    if (q === "") return items;
    return items.filter(
      (r) => r.name.toLowerCase().includes(q) || r.kind.toLowerCase().includes(q),
    );
  }, [resources.data, search]);

  const groups = useMemo(() => {
    const map = new Map<ResourceGroup, typeof filtered>();
    for (const r of filtered) {
      const g = groupOfKind(r.kind);
      const list = map.get(g) ?? [];
      list.push(r);
      map.set(g, list);
    }
    return [...map.entries()];
  }, [filtered]);

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Resource discovery"
        description={`Host paths, containers, volumes, and databases reported by agent ${agent.data?.hostname ?? agentId ?? "unknown"}.`}
        action={
          <Button type="button" variant="outline" asChild>
            <Link to="/agents">Back to agents</Link>
          </Button>
        }
      />
      <div className="flex flex-wrap items-center gap-3">
        <Input
          type="search"
          placeholder="Search resources…"
          aria-label="Search resources"
          className="max-w-sm"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        <Select
          aria-label="Filter by kind"
          className="w-auto"
          value={kind}
          onChange={(e) => setKind(e.target.value)}
        >
          <option value="">All kinds</option>
          {KIND_OPTIONS.map((k) => (
            <option key={k} value={k}>
              {k}
            </option>
          ))}
        </Select>
      </div>
      {resources.isLoading ? (
        <Card>
          <CardContent className="pt-6">
            <TableSkeleton rows={6} cols={3} />
          </CardContent>
        </Card>
      ) : resources.data === undefined ? (
        <Card>
          <CardContent className="pt-6">
            <p className="text-sm text-destructive">
              Could not load resources: {errorText(resources.error)}
            </p>
          </CardContent>
        </Card>
      ) : filtered.length === 0 ? (
        <Card>
          <CardContent className="pt-6">
            <p className="text-sm text-gray-500">
              No discovered resources{kind !== "" || search.trim() !== "" ? " match these filters" : " yet — the agent reports inventory on heartbeat"}.
            </p>
          </CardContent>
        </Card>
      ) : (
        groups.map(([group, items]) => {
          const Icon = GROUP_ICON[group];
          return (
            <Card key={group}>
              <CardHeader>
                <CardTitle>
                  <span className="flex items-center gap-2">
                    <Icon className="size-4" aria-hidden />
                    {resourceGroupLabel(group)} ({items.length})
                  </span>
                </CardTitle>
              </CardHeader>
              <CardContent>
                <div className="overflow-x-auto">
                  <table className="w-full text-left text-sm">
                    <thead>
                      <tr className="border-b border-border text-xs text-gray-500">
                        <th className="py-2 pr-4 font-medium">Name</th>
                        <th className="py-2 pr-4 font-medium">Kind</th>
                        <th className="py-2 pr-4 font-medium">Labels</th>
                        <th className="py-2 pr-4 font-medium">Last seen</th>
                      </tr>
                    </thead>
                    <tbody>
                      {items.map((r) => (
                        <tr key={r.id} className="border-b border-border">
                          <td className="py-2 pr-4 font-mono text-xs">{r.name}</td>
                          <td className="py-2 pr-4">
                            <span className="inline-flex items-center rounded-full border border-border bg-surface px-2.5 py-0.5 text-xs">
                              {r.kind}
                            </span>
                          </td>
                          <td className="py-2 pr-4 text-xs text-gray-500">
                            {r.detail === undefined || Object.keys(r.detail).length === 0
                              ? "—"
                              : Object.entries(r.detail)
                                  .map(([k, v]) => `${k}=${v}`)
                                  .join(", ")}
                          </td>
                          <td className="py-2 pr-4" title={formatDateTime(r.last_seen_at)}>
                            {formatDateTime(r.last_seen_at)}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </CardContent>
            </Card>
          );
        })
      )}
    </div>
  );
}
