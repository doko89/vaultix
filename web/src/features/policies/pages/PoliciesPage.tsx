import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Plus } from "lucide-react";
import { errorText } from "@/shared/lib/api";
import { useDebouncedValue } from "@/shared/hooks/use-debounce";
import { Button } from "@/shared/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { PageHeader } from "@/shared/ui/page-header";
import { Pagination } from "@/shared/ui/pagination";
import { TableSkeleton } from "@/shared/ui/skeleton";
import { usePolicies } from "../hooks";
import { PolicyEditor } from "./PolicyEditor";

const PAGE_SIZE = 25;

export function PoliciesPage() {
  const [search, setSearch] = useState("");
  const [offset, setOffset] = useState(0);
  const [createOpen, setCreateOpen] = useState(false);
  const navigate = useNavigate();
  const q = useDebouncedValue(search.trim());
  const policies = usePolicies({ q: q === "" ? undefined : q, limit: PAGE_SIZE, offset });

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Policies"
        description="Create, edit, assign, and disable backup policies."
        action={
          <Button type="button" onClick={() => setCreateOpen(true)}>
            <Plus aria-hidden />
            Create policy
          </Button>
        }
      />
      <div className="flex flex-wrap items-center gap-3">
        <Input
          type="search"
          placeholder="Search policies…"
          aria-label="Search policies"
          className="max-w-sm"
          value={search}
          onChange={(e) => {
            setSearch(e.target.value);
            setOffset(0);
          }}
        />
      </div>
      <Card>
        <CardHeader>
          <CardTitle>All policies</CardTitle>
        </CardHeader>
        <CardContent>
          {policies.isLoading ? (
            <TableSkeleton rows={6} cols={4} />
          ) : policies.data === undefined ? (
            <p className="text-sm text-destructive">Could not load policies: {errorText(policies.error)}</p>
          ) : policies.data.items.length === 0 ? (
            <p className="text-sm text-gray-500">No policies yet. Create your first policy to get started.</p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm">
                <thead>
                  <tr className="border-b border-border text-xs text-gray-500">
                    <th className="py-2 pr-4 font-medium">Name</th>
                    <th className="py-2 pr-4 font-medium">Agents</th>
                    <th className="py-2 pr-4 font-medium">Version</th>
                    <th className="py-2 pr-4 font-medium">Encryption</th>
                    <th className="py-2 pr-4 font-medium">State</th>
                  </tr>
                </thead>
                <tbody>
                  {policies.data.items.map((policy) => (
                    <tr
                      key={policy.id}
                      className="cursor-pointer border-b border-border hover:bg-surface"
                      onClick={() => navigate(`/policies/${encodeURIComponent(policy.id)}`)}
                    >
                      <td className="py-2 pr-4 font-medium">{policy.name}</td>
                      <td className="py-2 pr-4">{policy.agent_ids.length}</td>
                      <td className="py-2 pr-4">v{policy.current_version}</td>
                      <td className="py-2 pr-4 text-xs">
                        {policy.encryption.enabled ? `Encrypted · key ${policy.encryption.key_ref}` : "Unencrypted"}
                      </td>
                      <td className="py-2 pr-4">
                        <span
                          className={
                            policy.enabled
                              ? "inline-flex items-center rounded-full border border-success/30 bg-success/10 px-2.5 py-0.5 text-xs font-medium text-success"
                              : "inline-flex items-center rounded-full border border-border bg-surface px-2.5 py-0.5 text-xs font-medium text-gray-500"
                          }
                        >
                          {policy.enabled ? "Enabled" : "Disabled"}
                        </span>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          {policies.data !== undefined && policies.data.total > 0 && (
            <Pagination total={policies.data.total} limit={PAGE_SIZE} offset={offset} onChange={setOffset} />
          )}
        </CardContent>
      </Card>

      <PolicyEditor open={createOpen} policy={undefined} onClose={() => setCreateOpen(false)} />
    </div>
  );
}
