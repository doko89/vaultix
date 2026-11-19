import { useEffect, useState } from "react";
import { Plus } from "lucide-react";
import { Button } from "@/shared/ui/button";
import { Dialog } from "@/shared/ui/dialog";
import { Input } from "@/shared/ui/input";
import { Select } from "@/shared/ui/select";
import { Textarea } from "@/shared/ui/textarea";
import {
  useAgentOptions,
  useCreatePolicy,
  useCredentialOptions,
  usePolicyVersion,
  useStorageOptions,
  useUpdatePolicy,
} from "../hooks";
import type { Policy, PolicyInput, TargetInput } from "../types";
import { emptyTarget, TargetForm, TargetRemoveButton } from "./TargetForm";
import { validatePolicy, validateTarget, withMethods } from "../validate";

// Full create/edit dialog. PUT is a full replace that also publishes a new
// version, so edit pre-fills from the policy plus its current targets.

export interface PolicyEditorProps {
  open: boolean;
  policy?: Policy | undefined;
  onClose: () => void;
}

function fromPolicy(
  policy: Policy,
  current: { retention: PolicyInput["retention"]; targets: TargetInput[] } | undefined,
): PolicyInput {
  return {
    name: policy.name,
    description: policy.description,
    agent_ids: [...policy.agent_ids],
    storage_id: policy.storage_id,
    compression: { ...policy.compression },
    encryption: { ...policy.encryption },
    retention: current?.retention ?? { keep_last_n: 7, keep_days: 0, keep_minimum: 1 },
    targets: current !== undefined && current.targets.length > 0 ? current.targets : [emptyTarget()],
    note: "",
  };
}

function blankPolicy(): PolicyInput {
  return {
    name: "",
    description: "",
    agent_ids: [],
    storage_id: "",
    compression: { algorithm: "", level: 0 },
    encryption: { enabled: false, key_ref: "" },
    retention: { keep_last_n: 7, keep_days: 0, keep_minimum: 1 },
    targets: [emptyTarget()],
    note: "",
  };
}

export function PolicyEditor({ open, policy, onClose }: PolicyEditorProps) {
  const editing = policy !== undefined;
  const [form, setForm] = useState<PolicyInput>(blankPolicy());
  const [showErrors, setShowErrors] = useState(false);
  const currentVersion = usePolicyVersion(
    editing ? policy.id : undefined,
    editing ? policy.current_version : undefined,
  );

  const agents = useAgentOptions();
  const storages = useStorageOptions();
  const credentials = useCredentialOptions();
  const encryptionKeys = (credentials.data ?? []).filter((c) => c.kind === "encryption");

  const create = useCreatePolicy(onClose);
  const update = useUpdatePolicy(onClose);

  useEffect(() => {
    if (!open) return;
    setShowErrors(false);
    if (policy === undefined) {
      setForm(blankPolicy());
      return;
    }
    const version = currentVersion.data;
    const targets: TargetInput[] = (version?.targets ?? []).map((t) => ({
      type: t.type as TargetInput["type"],
      method: t.method,
      selector: t.selector,
      include: t.include ?? [],
      exclude: t.exclude ?? [],
      options: t.options ?? {},
      credential_ref: t.credential_ref === "" ? undefined : t.credential_ref,
    }));
    setForm(
      fromPolicy(
        policy,
        version === undefined
          ? undefined
          : {
              retention: {
                keep_last_n: version.retention.keep_last_n,
                keep_days: version.retention.keep_days,
                keep_minimum: version.retention.keep_minimum,
              },
              targets,
            },
      ),
    );
  }, [open, policy, currentVersion.data]);

  const errors = showErrors ? validatePolicy({ ...form, targets: withMethods(form.targets) }) : [];
  const busy = create.isPending || update.isPending;

  function patch(next: Partial<PolicyInput>): void {
    setForm((prev) => ({ ...prev, ...next }));
  }

  function setTarget(index: number, next: TargetInput): void {
    setForm((prev) => ({
      ...prev,
      targets: prev.targets.map((t, i) => (i === index ? next : t)),
    }));
  }

  function submit(): void {
    setShowErrors(true);
    const input: PolicyInput = {
      ...form,
      name: form.name.trim(),
      description: form.description?.trim() === "" ? undefined : form.description?.trim(),
      storage_id: form.storage_id === "" ? undefined : form.storage_id,
      note: form.note?.trim() === "" ? undefined : form.note?.trim(),
      targets: withMethods(form.targets),
    };
    if (validatePolicy(input).length > 0) return;
    if (policy === undefined) {
      create.mutate(input);
    } else {
      update.mutate({ policyId: policy.id, input });
    }
  }

  const targetErrors = (index: number): boolean =>
    showErrors && validateTarget(withMethods(form.targets)[index] ?? emptyTarget()).length > 0;

  return (
    <Dialog
      open={open}
      title={editing ? `Edit policy` : "Create policy"}
      description={
        editing
          ? "Saving publishes a new immutable version; running jobs keep the old one."
          : "Policies target one or more agents and start at version 1."
      }
      onClose={onClose}
      wide
    >
      <div className="flex max-h-[70vh] flex-col gap-4 overflow-y-auto pr-1">
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div>
            <label className="mb-1 block text-sm font-medium">Name</label>
            <Input value={form.name} onChange={(e) => patch({ name: e.target.value })} placeholder="Nightly app backups" />
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium">Destination</label>
            <Select value={form.storage_id ?? ""} onChange={(e) => patch({ storage_id: e.target.value })}>
              <option value="">(default destination)</option>
              {(storages.data ?? []).map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
            </Select>
          </div>
        </div>

        <div>
          <label className="mb-1 block text-sm font-medium">Description</label>
          <Textarea
            value={form.description ?? ""}
            onChange={(e) => patch({ description: e.target.value })}
            placeholder="What this policy protects and why"
          />
        </div>

        <div>
          <span className="mb-1 block text-sm font-medium">Agents (at least one)</span>
          {agents.isLoading ? (
            <p className="text-sm text-gray-500">Loading agents…</p>
          ) : (agents.data ?? []).length === 0 ? (
            <p className="text-sm text-gray-500">No agents enrolled yet.</p>
          ) : (
            <div className="flex max-h-32 flex-col gap-1 overflow-y-auto rounded-md border border-border p-2">
              {(agents.data ?? []).map((a) => (
                <label key={a.id} className="flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={form.agent_ids.includes(a.id)}
                    onChange={(e) =>
                      patch({
                        agent_ids: e.target.checked
                          ? [...form.agent_ids, a.id]
                          : form.agent_ids.filter((id) => id !== a.id),
                      })
                    }
                  />
                  {a.hostname}
                </label>
              ))}
            </div>
          )}
        </div>

        <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
          <div>
            <label className="mb-1 block text-sm font-medium">Keep last N</label>
            <Input
              inputMode="numeric"
              value={String(form.retention.keep_last_n)}
              onChange={(e) => patch({ retention: { ...form.retention, keep_last_n: Number(e.target.value) || 0 } })}
            />
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium">Keep days</label>
            <Input
              inputMode="numeric"
              value={String(form.retention.keep_days)}
              onChange={(e) => patch({ retention: { ...form.retention, keep_days: Number(e.target.value) || 0 } })}
            />
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium">Keep minimum</label>
            <Input
              inputMode="numeric"
              value={String(form.retention.keep_minimum)}
              onChange={(e) => patch({ retention: { ...form.retention, keep_minimum: Number(e.target.value) || 0 } })}
            />
          </div>
        </div>

        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div>
            <label className="mb-1 block text-sm font-medium">Compression</label>
            <Select
              value={form.compression.algorithm === "" ? "default" : form.compression.algorithm}
              onChange={(e) =>
                patch({
                  compression: {
                    ...form.compression,
                    algorithm: e.target.value === "default" ? "" : e.target.value,
                  },
                })
              }
            >
              <option value="default">zstd (default)</option>
              <option value="zstd">zstd</option>
            </Select>
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium">Compression level (0–22, 0 = default)</label>
            <Input
              inputMode="numeric"
              value={String(form.compression.level)}
              onChange={(e) => patch({ compression: { ...form.compression, level: Number(e.target.value) || 0 } })}
            />
          </div>
        </div>

        <div className="rounded-md border border-border p-3">
          <label className="flex items-center gap-2 text-sm font-medium">
            <input
              type="checkbox"
              checked={form.encryption.enabled}
              onChange={(e) => patch({ encryption: { ...form.encryption, enabled: e.target.checked } })}
            />
            Enable encryption
          </label>
          {form.encryption.enabled && (
            <div className="mt-2">
              <label className="mb-1 block text-sm font-medium">Encryption key</label>
              <Select
                value={form.encryption.key_ref}
                onChange={(e) => patch({ encryption: { ...form.encryption, key_ref: e.target.value } })}
              >
                <option value="">Select a key…</option>
                {encryptionKeys.map((c) => (
                  <option key={c.id} value={c.key_ref !== "" ? c.key_ref : c.id}>
                    {c.name}
                  </option>
                ))}
              </Select>
              {encryptionKeys.length === 0 && (
                <p className="mt-1 text-xs text-gray-500">
                  No encryption profiles yet — create one under Credentials first.
                </p>
              )}
            </div>
          )}
        </div>

        <div className="flex flex-col gap-3">
          <div className="flex items-center justify-between">
            <span className="text-sm font-medium">Targets</span>
            <Button type="button" variant="outline" size="sm" onClick={() => patch({ targets: [...form.targets, emptyTarget()] })}>
              <Plus aria-hidden />
              Add target
            </Button>
          </div>
          {form.targets.map((t, i) => (
            <div key={i} className="flex flex-col gap-1">
              <div className="flex items-center justify-between">
                <span className="text-xs font-medium text-gray-500">
                  Target {i + 1}
                  {targetErrors(i) ? " — needs attention" : ""}
                </span>
                {form.targets.length > 1 && (
                  <TargetRemoveButton
                    onRemove={() => patch({ targets: form.targets.filter((_, j) => j !== i) })}
                  />
                )}
              </div>
              <TargetForm value={t} onChange={(next) => setTarget(i, next)} showErrors={showErrors} />
            </div>
          ))}
        </div>

        <div>
          <label className="mb-1 block text-sm font-medium">Version note</label>
          <Input
            value={form.note ?? ""}
            onChange={(e) => patch({ note: e.target.value })}
            placeholder="Why this version exists"
          />
        </div>

        {errors.length > 0 && (
          <ul className="flex flex-col gap-1 rounded-md border border-destructive/30 bg-destructive/10 p-3 text-xs text-destructive">
            {errors.map((e, i) => (
              <li key={i}>{e}</li>
            ))}
          </ul>
        )}

        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button type="button" onClick={submit} disabled={busy}>
            {busy ? "Saving…" : editing ? "Save (new version)" : "Create policy"}
          </Button>
        </div>
      </div>
    </Dialog>
  );
}
