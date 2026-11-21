import { Link } from "react-router-dom";
import type { JobState } from "@/shared/types";
import { errorText } from "@/shared/lib/api";
import { formatBytes, formatDateTime } from "@/shared/lib/format";
import { StateBadge } from "@/shared/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/shared/ui/card";
import { PageHeader } from "@/shared/ui/page-header";
import { CardSkeleton } from "@/shared/ui/skeleton";
import { useDashboard } from "../hooks";
import type { Dashboard, DayPoint } from "../types";

function TrendChart({ trend }: { trend: DayPoint[] }) {
  const max = Math.max(1, ...trend.map((d) => d.succeeded + d.failed));
  return (
    <div className="flex min-h-28 flex-1 items-end gap-1" role="img" aria-label="Jobs per day for the last two weeks">
      {trend.map((d) => {
        const total = d.succeeded + d.failed;
        const heightPct = Math.max(total === 0 ? 2 : (total / max) * 100, 2);
        const failedPct = total === 0 ? 0 : (d.failed / total) * 100;
        return (
          <div
            key={d.date}
            className="flex flex-1 flex-col justify-end overflow-hidden rounded-sm bg-success/30"
            style={{ height: `${heightPct}%` }}
            title={`${d.date}: ${d.succeeded} succeeded, ${d.failed} failed, ${formatBytes(d.bytes_uploaded)} uploaded`}
          >
            {failedPct > 0 && <div className="bg-destructive" style={{ height: `${failedPct}%` }} />}
          </div>
        );
      })}
    </div>
  );
}

function LoadedDashboard({ dashboard }: { dashboard: Dashboard }) {
  const failed = dashboard.jobs_by_status["failed"] ?? 0;
  const stats = [
    { label: "Total agents", value: String(dashboard.agents.total) },
    { label: "Online", value: String(dashboard.agents.online) },
    { label: "Offline", value: String(dashboard.agents.offline) },
    { label: "Failed jobs", value: String(failed) },
  ];

  return (
    <div className="flex flex-col gap-6">
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {stats.map((stat) => (
          <Card key={stat.label}>
            <CardHeader>
              <CardTitle>{stat.label}</CardTitle>
            </CardHeader>
            <CardContent>
              <p className="text-2xl font-semibold">{stat.value}</p>
            </CardContent>
          </Card>
        ))}
      </div>

      <div className="grid grid-cols-1 gap-6 xl:grid-cols-2">
        <Card className="flex flex-col">
          <CardHeader>
            <CardTitle>Recent jobs</CardTitle>
          </CardHeader>
          <CardContent>
            {dashboard.recent_jobs.length === 0 ? (
              <p className="text-sm text-gray-500">No jobs yet.</p>
            ) : (
              <ul className="flex flex-col gap-2">
                {dashboard.recent_jobs.map((job) => (
                  <li key={job.id} className="flex items-center justify-between gap-2 text-sm">
                    <span className="flex min-w-0 items-center gap-2">
                      <StateBadge state={job.state} />
                      <Link to="/jobs" className="truncate font-mono text-xs text-primary hover:underline">
                        {job.id}
                      </Link>
                    </span>
                    <span className="shrink-0 text-xs text-gray-500">
                      {formatDateTime(job.finished_at ?? job.started_at)}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>

        <Card className="flex flex-col">
          <CardHeader>
            <CardTitle>Backup trends (14 days)</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-1 flex-col">
            {dashboard.trend.length === 0 ? (
              <p className="text-sm text-gray-500">No trend data yet.</p>
            ) : (
              <>
                <TrendChart trend={dashboard.trend} />
                <div className="mt-2 flex gap-4 text-xs text-gray-500">
                  <span className="flex items-center gap-1">
                    <span className="size-2 rounded-sm bg-success/60" aria-hidden /> succeeded
                  </span>
                  <span className="flex items-center gap-1">
                    <span className="size-2 rounded-sm bg-destructive" aria-hidden /> failed
                  </span>
                </div>
              </>
            )}
          </CardContent>
        </Card>
      </div>

      <div className="grid grid-cols-1 gap-6 xl:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Latest backup per agent</CardTitle>
          </CardHeader>
          <CardContent>
            {dashboard.last_backup_per_agent.length === 0 ? (
              <p className="text-sm text-gray-500">No backups recorded.</p>
            ) : (
              <ul className="flex flex-col gap-2 text-sm">
                {dashboard.last_backup_per_agent.map((b, i) => (
                  <li key={`${b.agent_id ?? "?"}-${i}`} className="flex items-center justify-between gap-2">
                    <span className="min-w-0 truncate">{b.agent_name !== "" && b.agent_name !== undefined ? b.agent_name : (b.agent_id ?? "—")}</span>
                    <span className="flex shrink-0 items-center gap-2 text-xs text-gray-500">
                      {b.state !== undefined ? (
                        <StateBadge state={b.state as JobState} />
                      ) : b.has_succeeded ? (
                        "succeeded"
                      ) : (
                        "never"
                      )}
                      {formatDateTime(b.finished_at)}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Latest backup per policy</CardTitle>
          </CardHeader>
          <CardContent>
            {dashboard.last_backup_per_policy.length === 0 ? (
              <p className="text-sm text-gray-500">No backups recorded.</p>
            ) : (
              <ul className="flex flex-col gap-2 text-sm">
                {dashboard.last_backup_per_policy.map((b, i) => (
                  <li key={`${b.policy_id ?? "?"}-${i}`} className="flex items-center justify-between gap-2">
                    <span className="min-w-0 truncate">{b.policy_name !== "" && b.policy_name !== undefined ? b.policy_name : (b.policy_id ?? "—")}</span>
                    <span className="flex shrink-0 items-center gap-2 text-xs text-gray-500">
                      {b.state !== undefined ? (
                        <StateBadge state={b.state as JobState} />
                      ) : b.has_succeeded ? (
                        "succeeded"
                      ) : (
                        "never"
                      )}
                      {formatDateTime(b.finished_at)}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>
      </div>

      <div className="grid grid-cols-1 gap-6 xl:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Policies without a recent successful backup</CardTitle>
          </CardHeader>
          <CardContent>
            {dashboard.policies_without_recent_success.length === 0 ? (
              <p className="text-sm text-gray-500">Every policy has a recent success. Nothing at risk.</p>
            ) : (
              <ul className="flex flex-col gap-2 text-sm">
                {dashboard.policies_without_recent_success.map((g) => (
                  <li key={g.policy_id} className="flex items-center justify-between gap-2">
                    <Link to="/policies" className="min-w-0 truncate text-primary hover:underline">
                      {g.policy_name !== "" && g.policy_name !== undefined ? g.policy_name : g.policy_id}
                    </Link>
                    <span className="shrink-0 text-xs text-gray-500">
                      last success {formatDateTime(g.last_success_at)}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Storage</CardTitle>
          </CardHeader>
          <CardContent>
            {!dashboard.storage.available ? (
              <p className="text-sm text-gray-500">Storage usage is not reported by this Manager.</p>
            ) : (
              <dl className="grid grid-cols-1 gap-2 text-sm sm:grid-cols-2">
                <div className="flex justify-between gap-2">
                  <dt className="text-gray-500">Used</dt>
                  <dd>{formatBytes(dashboard.storage.bytes_used)}</dd>
                </div>
                <div className="flex justify-between gap-2">
                  <dt className="text-gray-500">Objects</dt>
                  <dd>{dashboard.storage.object_count ?? "—"}</dd>
                </div>
                <div className="flex justify-between gap-2">
                  <dt className="text-gray-500">Snapshots</dt>
                  <dd>{dashboard.storage.snapshot_count ?? "—"}</dd>
                </div>
              </dl>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

export function DashboardPage() {
  const dashboard = useDashboard();

  return (
    <div className="flex flex-col gap-6">
      <PageHeader title="Dashboard" description="System overview and backup health across all agents." />
      {dashboard.isLoading ? (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
          {[0, 1, 2, 3].map((i) => (
            <Card key={i}>
              <CardContent className="pt-6">
                <CardSkeleton />
              </CardContent>
            </Card>
          ))}
        </div>
      ) : dashboard.data === undefined ? (
        <Card>
          <CardContent className="pt-6">
            <p className="text-sm text-destructive">Could not load dashboard: {errorText(dashboard.error)}</p>
          </CardContent>
        </Card>
      ) : (
        <LoadedDashboard dashboard={dashboard.data} />
      )}
    </div>
  );
}
