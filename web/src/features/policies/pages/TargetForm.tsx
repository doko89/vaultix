import { Button } from "@/shared/ui/button";
import { Input } from "@/shared/ui/input";
import { Select } from "@/shared/ui/select";
import { useCredentialOptions } from "../hooks";
import { TARGET_METHOD, TARGET_TYPE_LABEL, TARGET_TYPES } from "../types";
import type { TargetInput, TargetType } from "../types";
import { validateTarget } from "../validate";

// One target-selection form. Fields adapt to the target type; rules mirror
// the server (internal/usecase/policy/validate.go). Used by the policy
// editor and the standalone add/edit-target dialogs.

export interface TargetFormProps {
  value: TargetInput;
  onChange: (next: TargetInput) => void;
  showErrors: boolean;
}

function setOption(value: TargetInput, key: string, opt: string | undefined): TargetInput {
  const options = { ...(value.options ?? {}) };
  if (opt === undefined || opt === "") {
    delete options[key];
  } else {
    options[key] = opt;
  }
  return { ...value, options };
}

function BoolRow({
  label,
  value,
  onChange,
}: {
  label: string;
  value: string | undefined;
  onChange: (next: string | undefined) => void;
}) {
  return (
    <div>
      <label className="mb-1 block text-sm font-medium">{label}</label>
      <Select
        value={value ?? ""}
        onChange={(e) => onChange(e.target.value === "" ? undefined : e.target.value)}
      >
        <option value="">(default)</option>
        <option value="true">true</option>
        <option value="false">false</option>
      </Select>
    </div>
  );
}

export function emptyTarget(): TargetInput {
  return { type: "host_filesystem", selector: "" };
}

export function TargetForm({ value, onChange, showErrors }: TargetFormProps) {
  const credentials = useCredentialOptions();
  const errors = showErrors ? validateTarget(value) : [];
  const profiles = (credentials.data ?? []).filter((c) => c.kind === "database" || c.kind === "generic");

  const needsCredential = value.type === "postgres" || value.type === "mysql" || value.type === "mssql";
  const forbidsCredential =
    value.type === "host_filesystem" ||
    value.type === "docker_config" ||
    value.type === "docker_volume" ||
    value.type === "app_stack";

  return (
    <div className="flex flex-col gap-3 rounded-md border border-border p-3">
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <div>
          <label className="mb-1 block text-sm font-medium">Target type</label>
          <Select
            value={value.type}
            onChange={(e) => {
              const type = e.target.value as TargetType;
              onChange({ ...value, type, credential_ref: undefined, options: {}, include: [], exclude: [] });
            }}
          >
            {TARGET_TYPES.map((t) => (
              <option key={t} value={t}>
                {TARGET_TYPE_LABEL[t]}
              </option>
            ))}
          </Select>
        </div>
        <div>
          <label className="mb-1 block text-sm font-medium">Method</label>
          <Input value={TARGET_METHOD[value.type] ?? "full"} disabled aria-label="Backup method (fixed per type)" />
        </div>
      </div>

      <div>
        <label className="mb-1 block text-sm font-medium">
          {value.type === "host_filesystem"
            ? "Absolute path"
            : value.type === "postgres" || value.type === "mysql" || value.type === "mssql"
              ? "Database name"
              : value.type === "app_stack"
                ? "Stack ID"
                : value.type === "redis"
                  ? "Instance label or persistence path"
                  : "Selector (name, ID, or path)"}
        </label>
        <Input
          value={value.selector}
          onChange={(e) => onChange({ ...value, selector: e.target.value })}
          placeholder={
            value.type === "host_filesystem"
              ? "/var/lib/app"
              : value.type === "docker_volume"
                ? "app-data"
                : value.type === "app_stack"
                  ? "billing-stack"
                  : "name"
          }
          className="font-mono"
        />
      </div>

      {!forbidsCredential && (
        <div>
          <label className="mb-1 block text-sm font-medium">
            Credential profile{needsCredential ? " (required)" : " (optional)"}
          </label>
          <Select
            value={value.credential_ref ?? ""}
            onChange={(e) => onChange({ ...value, credential_ref: e.target.value === "" ? undefined : e.target.value })}
          >
            <option value="">{needsCredential ? "Select a profile…" : "(none)"}</option>
            {profiles.map((c) => (
              <option key={c.id} value={c.key_ref !== "" ? c.key_ref : c.id}>
                {c.name} ({c.kind})
              </option>
            ))}
          </Select>
          {credentials.isLoading && <p className="mt-1 text-xs text-gray-500">Loading profiles…</p>}
        </div>
      )}

      {value.type === "host_filesystem" && (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div>
            <label className="mb-1 block text-sm font-medium">Include globs (comma-separated)</label>
            <Input
              value={(value.include ?? []).join(", ")}
              onChange={(e) =>
                onChange({ ...value, include: splitList(e.target.value) })
              }
              placeholder="*.db, uploads/**"
              className="font-mono"
            />
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium">Exclude globs (comma-separated)</label>
            <Input
              value={(value.exclude ?? []).join(", ")}
              onChange={(e) =>
                onChange({ ...value, exclude: splitList(e.target.value) })
              }
              placeholder="*.tmp, cache/**"
              className="font-mono"
            />
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium">Symlink handling</label>
            <Select
              value={value.options?.["symlink"] ?? ""}
              onChange={(e) => onChange(setOption(value, "symlink", e.target.value === "" ? undefined : e.target.value))}
            >
              <option value="">(default)</option>
              <option value="follow">follow</option>
              <option value="skip">skip</option>
              <option value="record">record</option>
            </Select>
          </div>
          <BoolRow
            label="One file system"
            value={value.options?.["one_file_system"]}
            onChange={(next) => onChange(setOption(value, "one_file_system", next))}
          />
        </div>
      )}

      {value.type === "docker_config" && (
        <BoolRow
          label="Include compose files"
          value={value.options?.["include_compose"]}
          onChange={(next) => onChange(setOption(value, "include_compose", next))}
        />
      )}

      {(value.type === "postgres" || value.type === "mysql") && (
        <div>
          <label className="mb-1 block text-sm font-medium">Dump scope</label>
          <Select
            value={value.options?.["scope"] ?? ""}
            onChange={(e) => onChange(setOption(value, "scope", e.target.value === "" ? undefined : e.target.value))}
          >
            <option value="">(default)</option>
            <option value="database">database</option>
            <option value="cluster">cluster</option>
          </Select>
        </div>
      )}

      {value.type === "redis" && (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div>
            <label className="mb-1 block text-sm font-medium">Persistence</label>
            <Select
              value={value.options?.["persistence"] ?? ""}
              onChange={(e) => onChange(setOption(value, "persistence", e.target.value === "" ? undefined : e.target.value))}
            >
              <option value="">(default)</option>
              <option value="rdb">rdb</option>
              <option value="aof">aof</option>
              <option value="both">both</option>
            </Select>
          </div>
          <BoolRow
            label="Verify after backup"
            value={value.options?.["verify"]}
            onChange={(next) => onChange(setOption(value, "verify", next))}
          />
        </div>
      )}

      {errors.length > 0 && (
        <ul className="flex flex-col gap-1 text-xs text-destructive">
          {errors.map((e, i) => (
            <li key={i}>{e}</li>
          ))}
        </ul>
      )}
    </div>
  );
}

function splitList(raw: string): string[] {
  return raw
    .split(",")
    .map((s) => s.trim())
    .filter((s) => s !== "");
}

export function TargetRemoveButton({ onRemove }: { onRemove: () => void }) {
  return (
    <Button type="button" variant="ghost" size="sm" onClick={onRemove}>
      Remove target
    </Button>
  );
}
