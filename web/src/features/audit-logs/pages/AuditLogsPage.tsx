import { useState } from "react";
import { errorText } from "@/shared/lib/api";
import { formatDateTime } from "@/shared/lib/format";
import { Card, CardContent, CardHeader, CardTitle } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { PageHeader } from "@/shared/ui/page-header";
import { Pagination } from "@/shared/ui/pagination";
import { Select } from "@/shared/ui/select";
import { TableSkeleton } from "@/shared/ui/skeleton";
import { useAuditEntries } from "../hooks";
import { AUDIT_ACTIONS } from "../types";

const PAGE_SIZE = 25;

export function AuditLogsPage() {
  const [actor, setActor] = useState("");
  const [action, setAction] = useState("");
  const [outcome, setOutcome] = useState("");
  const [offset, setOffset] = useState(0);

  const entries = useAuditEntries({
    actor: actor.trim() === "" ? undefined : actor.trim(),
    action: action === "" ? undefined : action,
    outcome: outcome === "" ? undefined : outcome,
    limit: PAGE_SIZE,
    offset,
  });

  function resetOffset(): void {
    setOffset(0);
  }

  return (
    <div className="flex flex-col gap-6">
      <PageHeader title="Audit Logs" description="Review administrative and security events." />
      <div className="flex flex-wrap items-center gap-3">
        <Input
          type="search"
          placeholder="Filter by actor ID…"
          aria-label="Filter by actor"
          className="max-w-[12rem] font-mono"
          value={actor}
          onChange={(e) => {
            setActor(e.target.value);
            resetOffset();
          }}
        />
        <Select
          aria-label="Filter by action"
          className="w-auto"
          value={action}
          onChange={(e) => {
            setAction(e.target.value);
            resetOffset();
          }}
        >
          <option value="">All actions</option>
          {AUDIT_ACTIONS.map((a) => (
            <option key={a} value={a}>
              {a}
            </option>
          ))}
        </Select>
        <Select
          aria-label="Filter by outcome"
          className="w-auto"
          value={outcome}
          onChange={(e) => {
            setOutcome(e.target.value);
            resetOffset();
          }}
        >
          <option value="">All outcomes</option>
          <option value="success">success</option>
          <option value="failure">failure</option>
          <option value="denied">denied</option>
        </Select>
      </div>
      <Card>
        <CardHeader>
          <CardTitle>Events</CardTitle>
        </CardHeader>
        <CardContent>
          {entries.isLoading ? (
            <TableSkeleton rows={6} cols={5} />
          ) : entries.data === undefined ? (
            <p className="text-sm text-destructive">Could not load audit logs: {errorText(entries.error)}</p>
          ) : entries.data.items.length === 0 ? (
            <p className="text-sm text-gray-500">No events match these filters.</p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm">
                <thead>
                  <tr className="border-b border-border text-xs text-gray-500">
                    <th className="py-2 pr-4 font-medium">Time</th>
                    <th className="py-2 pr-4 font-medium">Actor</th>
                    <th className="py-2 pr-4 font-medium">Action</th>
                    <th className="py-2 pr-4 font-medium">Target</th>
                    <th className="py-2 pr-4 font-medium">Outcome</th>
                    <th className="py-2 pr-4 font-medium">Detail</th>
                  </tr>
                </thead>
                <tbody>
                  {entries.data.items.map((e) => (
                    <tr key={e.id} className="border-b border-border">
                      <td className="py-2 pr-4 text-xs">{formatDateTime(e.created_at)}</td>
                      <td className="py-2 pr-4 font-mono text-xs" title={e.actor_type}>
                        {e.actor_id ?? "—"}
                      </td>
                      <td className="py-2 pr-4 font-mono text-xs">{e.action}</td>
                      <td className="py-2 pr-4 font-mono text-xs">
                        {[e.target_type, e.target_id].filter((s) => s !== undefined && s !== "").join(" ") || "—"}
                      </td>
                      <td className="py-2 pr-4">
                        <span
                          className={
                            e.outcome === "success"
                              ? "inline-flex items-center rounded-full border border-success/30 bg-success/10 px-2.5 py-0.5 text-xs font-medium text-success"
                              : "inline-flex items-center rounded-full border border-destructive/30 bg-destructive/10 px-2.5 py-0.5 text-xs font-medium text-destructive"
                          }
                        >
                          {e.outcome}
                        </span>
                      </td>
                      <td className="max-w-[15rem] truncate py-2 pr-4 text-xs text-gray-500" title={e.detail ?? ""}>
                        {e.detail ?? "—"}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          {entries.data !== undefined && entries.data.total > 0 && (
            <Pagination total={entries.data.total} limit={PAGE_SIZE} offset={offset} onChange={setOffset} />
          )}
        </CardContent>
      </Card>
    </div>
  );
}
