import { useState } from "react";
import { Plus } from "lucide-react";
import type { ID } from "@/shared/types";
import { errorText } from "@/shared/lib/api";
import { formatDateTime } from "@/shared/lib/format";
import { useDebouncedValue } from "@/shared/hooks/use-debounce";
import { Button } from "@/shared/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/shared/ui/card";
import { ConfirmDialog } from "@/shared/ui/confirm-dialog";
import { Dialog } from "@/shared/ui/dialog";
import { Input } from "@/shared/ui/input";
import { PageHeader } from "@/shared/ui/page-header";
import { Pagination } from "@/shared/ui/pagination";
import { TableSkeleton } from "@/shared/ui/skeleton";
import {
  useDeletePolicy,
  usePolicies,
  usePolicy,
  usePolicyTargets,
  usePolicyVersion,
  usePolicyVersions,
  useSetPolicyEnabled,
  useTargetMutations,
} from "../hooks";
import { TARGET_TYPE_LABEL } from "../types";
import type { Policy, PolicyTarget, TargetInput, TargetType } from "../types";
import { emptyTarget, TargetForm } from "./TargetForm";
import { PolicyEditor } from "./PolicyEditor";
import { validateTarget, withMethods } from "../validate";

const PAGE_SIZE = 25;

function TargetDialog({
  open,
  title,
  initial,
  busy,
  onSubmit,
  onClose,
}: {
  open: boolean;
  title: string;
  initial: TargetInput;
  busy: boolean;
  onSubmit: (input: TargetInput) => void;
  onClose: () => void;
}) {
  const [value, setValue] = useState<TargetInput>(initial);
  const [showErrors, setShowErrors] = useState(false);

  return (
    <Dialog
      open={open}
      title={title}
      description="Saving publishes a new immutable policy version."
      onClose={() => {
        setShowErrors(false);
        setValue(initial);
        onClose();
      }}
      wide
    >
      <div className="flex flex-col gap-4">
        <TargetForm value={value} onChange={setValue} showErrors={showErrors} />
        <div className="flex justify-end gap-2">
          <Button
            type="button"
            variant="outline"
            onClick={() => {
              setShowErrors(false);
              setValue(initial);
              onClose();
            }}
            disabled={busy}
          >
            Cancel
          </Button>
          <Button
            type="button"
            disabled={busy}
            onClick={() => {
              setShowErrors(true);
              const input = withMethods([value])[0] ?? value;
              if (validateTarget(input).length > 0) return;
              onSubmit(input);
              setShowErrors(false);
              setValue(emptyTarget());
            }}
          >
            {busy ? "Saving…" : "Save target"}
          </Button>
        </div>
      </div>
    </Dialog>
  );
}

function VersionViewer({ policyId, version }: { policyId: ID; version: number }) {
  const detail = usePolicyVersion(policyId, version);
  if (detail.isLoading) return <p className="text-sm text-gray-500">Loading version…</p>;
  const v = detail.data;
  if (v === undefined) {
    return <p className="text-sm text-destructive">Could not load version: {errorText(detail.error)}</p>;
  }
  return (
    <div className="flex flex-col gap-2 text-sm">
      <p className="text-xs text-gray-500">
        Published {formatDateTime(v.created_at)} by {v.created_by || "—"}
        {v.note !== "" ? ` — ${v.note}` : ""}
      </p>
      <p>
        Retention: keep {v.retention.keep_last_n} / {v.retention.keep_days}d (min {v.retention.keep_minimum}) ·
        Compression: {v.compression.algorithm === "" ? "zstd" : v.compression.algorithm}/
        {v.compression.level} ·{" "}
        {v.encryption.enabled ? `Encrypted · key ${v.encryption.key_ref}` : "Unencrypted"}
      </p>
      <ul className="flex flex-col gap-1">
        {v.targets.map((t) => (
          <li key={t.id} className="flex flex-wrap items-center gap-2">
            <span className="inline-flex items-center rounded-full border border-border bg-surface px-2.5 py-0.5 text-xs">
              {TARGET_TYPE_LABEL[t.type as TargetType] ?? t.type}
            </span>
            <code className="font-mono text-xs">{t.selector}</code>
            <span className="text-xs text-gray-500">{t.method}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}

function PolicyDetail({ policyId, onEdit, onClose }: { policyId: ID; onEdit: () => void; onClose: () => void }) {
  const detail = usePolicy(policyId);
  const targets = usePolicyTargets(policyId);
  const versions = usePolicyVersions(policyId);
  const setEnabled = useSetPolicyEnabled();
  const remove = useDeletePolicy();
  const mutations = useTargetMutations(policyId, () => setTargetDialog(null));
  const [targetDialog, setTargetDialog] = useState<{ mode: "add" } | { mode: "edit"; target: PolicyTarget } | null>(null);
  const [viewVersion, setViewVersion] = useState<number | undefined>(undefined);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [confirmRemoveTarget, setConfirmRemoveTarget] = useState<PolicyTarget | undefined>(undefined);

  const policy: Policy | undefined = detail.data;

  return (
    <Card>
      <CardHeader>
        <div className="flex items-start justify-between gap-4">
          <CardTitle>{policy?.name ?? "Policy detail"}</CardTitle>
          <Button type="button" variant="ghost" size="sm" onClick={onClose}>
            Close
          </Button>
        </div>
      </CardHeader>
      <CardContent>
        {policy === undefined ? (
          <p className="text-sm text-gray-500">{detail.isLoading ? "Loading…" : "Could not load this policy."}</p>
        ) : (
          <div className="flex flex-col gap-5">
            <div className="flex flex-wrap items-center gap-2">
              <span
                className={
                  policy.enabled
                    ? "inline-flex items-center rounded-full border border-success/30 bg-success/10 px-2.5 py-0.5 text-xs font-medium text-success"
                    : "inline-flex items-center rounded-full border border-border bg-surface px-2.5 py-0.5 text-xs font-medium text-gray-500"
                }
              >
                {policy.enabled ? "Enabled" : "Disabled"}
              </span>
              <span className="text-xs text-gray-500">v{policy.current_version}</span>
              <span className="text-xs text-gray-500">
                {policy.encryption.enabled ? `Encrypted · key ${policy.encryption.key_ref}` : "Unencrypted"}
              </span>
            </div>

            {policy.description !== "" && <p className="text-sm text-gray-500">{policy.description}</p>}

            <div>
              <h3 className="mb-1 text-sm font-semibold">Current targets</h3>
              {targets.data === undefined ? (
                <p className="text-sm text-gray-500">{targets.isLoading ? "Loading…" : "Could not load targets."}</p>
              ) : targets.data.items.length === 0 ? (
                <p className="text-sm text-gray-500">No targets on the current version.</p>
              ) : (
                <ul className="flex flex-col gap-2">
                  {targets.data.items.map((t) => (
                    <li
                      key={t.id}
                      className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-border p-2 text-sm"
                    >
                      <span className="flex flex-wrap items-center gap-2">
                        <span className="inline-flex items-center rounded-full border border-border bg-surface px-2.5 py-0.5 text-xs">
                          {TARGET_TYPE_LABEL[t.type as TargetType] ?? t.type}
                        </span>
                        <code className="font-mono text-xs">{t.selector}</code>
                      </span>
                      <span className="flex gap-1">
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          onClick={() => setTargetDialog({ mode: "edit", target: t })}
                        >
                          Edit
                        </Button>
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          onClick={() => setConfirmRemoveTarget(t)}
                        >
                          Remove
                        </Button>
                      </span>
                    </li>
                  ))}
                </ul>
              )}
              <div className="mt-2">
                <Button type="button" variant="outline" size="sm" onClick={() => setTargetDialog({ mode: "add" })}>
                  <Plus aria-hidden />
                  Add target
                </Button>
              </div>
            </div>

            <div>
              <h3 className="mb-1 text-sm font-semibold">Version history</h3>
              {versions.data === undefined ? (
                <p className="text-sm text-gray-500">{versions.isLoading ? "Loading…" : "Could not load versions."}</p>
              ) : (
                <ol className="flex flex-col gap-1">
                  {[...versions.data.items]
                    .sort((a, b) => b.version - a.version)
                    .map((v) => (
                      <li key={v.version}>
                        <button
                          type="button"
                          className="flex w-full items-center justify-between gap-2 rounded-md px-2 py-1 text-left text-sm hover:bg-surface"
                          onClick={() => setViewVersion((prev) => (prev === v.version ? undefined : v.version))}
                        >
                          <span>
                            v{v.version}
                            {v.version === policy.current_version ? " (current)" : " (read-only)"}
                            {v.note !== "" ? ` — ${v.note}` : ""}
                          </span>
                          <span className="text-xs text-gray-500">{formatDateTime(v.created_at)}</span>
                        </button>
                        {viewVersion === v.version && (
                          <div className="ml-2 border-l-2 border-border pl-3 py-2">
                            <VersionViewer policyId={policyId} version={v.version} />
                          </div>
                        )}
                      </li>
                    ))}
                </ol>
              )}
            </div>

            <div className="flex flex-wrap gap-2">
              <Button type="button" variant="outline" size="sm" onClick={onEdit}>
                Edit policy
              </Button>
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={setEnabled.isPending}
                onClick={() => setEnabled.mutate({ policyId, enabled: !policy.enabled })}
              >
                {policy.enabled ? "Disable" : "Enable"}
              </Button>
              <Button type="button" variant="destructive" size="sm" onClick={() => setConfirmDelete(true)}>
                Delete
              </Button>
            </div>
          </div>
        )}
      </CardContent>

      <TargetDialog
        key={targetDialog?.mode === "edit" ? targetDialog.target.id : "new"}
        open={targetDialog !== null}
        title={targetDialog?.mode === "edit" ? "Edit target" : "Add target"}
        initial={
          targetDialog?.mode === "edit"
            ? {
                type: targetDialog.target.type as TargetType,
                method: targetDialog.target.method,
                selector: targetDialog.target.selector,
                include: targetDialog.target.include ?? [],
                exclude: targetDialog.target.exclude ?? [],
                options: targetDialog.target.options ?? {},
                credential_ref:
                  targetDialog.target.credential_ref === "" ? undefined : targetDialog.target.credential_ref,
              }
            : emptyTarget()
        }
        busy={mutations.add.isPending || mutations.update.isPending}
        onSubmit={(input) => {
          if (targetDialog?.mode === "edit") {
            mutations.update.mutate({ targetId: targetDialog.target.id, input });
          } else {
            mutations.add.mutate(input);
          }
        }}
        onClose={() => setTargetDialog(null)}
      />

      <ConfirmDialog
        open={confirmDelete}
        title="Delete policy?"
        body={`“${policy?.name ?? "This policy"}” and its schedules stop producing new jobs. Snapshots already taken are kept per retention.`}
        confirmLabel="Delete policy"
        requireName={policy?.name}
        busy={remove.isPending}
        onConfirm={() => {
          remove.mutate(policyId, { onSuccess: onClose });
        }}
        onClose={() => setConfirmDelete(false)}
      />
      <ConfirmDialog
        open={confirmRemoveTarget !== undefined}
        title="Remove target?"
        body={`“${confirmRemoveTarget?.selector ?? ""}” leaves the next published version. Existing snapshots are unaffected.`}
        confirmLabel="Remove target"
        busy={mutations.remove.isPending}
        onConfirm={() => {
          if (confirmRemoveTarget !== undefined) {
            mutations.remove.mutate(confirmRemoveTarget.id, {
              onSuccess: () => setConfirmRemoveTarget(undefined),
            });
          }
        }}
        onClose={() => setConfirmRemoveTarget(undefined)}
      />
    </Card>
  );
}

export function PoliciesPage() {
  const [search, setSearch] = useState("");
  const [offset, setOffset] = useState(0);
  const [selected, setSelected] = useState<ID | undefined>(undefined);
  const [editor, setEditor] = useState<{ open: boolean; policy?: Policy | undefined }>({ open: false });
  const q = useDebouncedValue(search.trim());
  const policies = usePolicies({ q: q === "" ? undefined : q, limit: PAGE_SIZE, offset });

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Policies"
        description="Create, edit, assign, and disable backup policies."
        action={
          <Button type="button" onClick={() => setEditor({ open: true })}>
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
                      className={
                        selected === policy.id
                          ? "cursor-pointer border-b border-border bg-primary/5"
                          : "cursor-pointer border-b border-border hover:bg-surface"
                      }
                      onClick={() => setSelected((prev) => (prev === policy.id ? undefined : policy.id))}
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

      {selected !== undefined && (
        <PolicyDetail
          policyId={selected}
          onEdit={() => {
            const policy = policies.data?.items.find((p) => p.id === selected);
            setEditor({ open: true, policy });
          }}
          onClose={() => setSelected(undefined)}
        />
      )}

      <PolicyEditor
        open={editor.open}
        policy={editor.policy}
        onClose={() => setEditor({ open: false })}
      />
    </div>
  );
}
