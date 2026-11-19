import { useEffect, useState } from "react";
import { Loader2, Plus } from "lucide-react";
import type { ID } from "@/shared/types";
import { errorText } from "@/shared/lib/api";
import { formatDateTime } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/shared/ui/card";
import { ConfirmDialog } from "@/shared/ui/confirm-dialog";
import { Dialog } from "@/shared/ui/dialog";
import { Input } from "@/shared/ui/input";
import { PageHeader } from "@/shared/ui/page-header";
import { Select } from "@/shared/ui/select";
import { TableSkeleton } from "@/shared/ui/skeleton";
import {
  useCreateDestination,
  useDeleteDestination,
  useStorageCredentialOptions,
  useStorageDestinations,
  useTestDestination,
  useUpdateDestination,
} from "../hooks";
import { validateDestination } from "../types";
import type { DestinationInput, StorageDestination, TestConnectionResult } from "../types";

function blankDestination(): DestinationInput {
  return { name: "", endpoint: "", region: "", bucket: "", prefix: "", credential_ref: "", use_path_style: false, default: false };
}

function DestinationDialog({
  open,
  destination,
  onClose,
}: {
  open: boolean;
  destination?: StorageDestination | undefined;
  onClose: () => void;
}) {
  const [form, setForm] = useState<DestinationInput>(blankDestination());
  const [showErrors, setShowErrors] = useState(false);
  const credentials = useStorageCredentialOptions();
  const create = useCreateDestination(onClose);
  const update = useUpdateDestination(onClose);

  useEffect(() => {
    if (!open) return;
    setShowErrors(false);
    if (destination === undefined) {
      setForm(blankDestination());
    } else {
      setForm({
        name: destination.name,
        endpoint: destination.endpoint,
        region: destination.region,
        bucket: destination.bucket,
        prefix: destination.prefix,
        credential_ref: destination.credential_ref,
        use_path_style: destination.use_path_style,
        default: destination.default,
      });
    }
  }, [open, destination]);

  const errors = showErrors ? validateDestination(form) : [];
  const busy = create.isPending || update.isPending;

  function patch(next: Partial<DestinationInput>): void {
    setForm((prev) => ({ ...prev, ...next }));
  }

  function submit(): void {
    setShowErrors(true);
    const input: DestinationInput = {
      ...form,
      name: form.name.trim(),
      endpoint: form.endpoint?.trim() === "" ? undefined : form.endpoint?.trim(),
      region: form.region?.trim() === "" ? undefined : form.region?.trim(),
      bucket: form.bucket.trim(),
      prefix: form.prefix?.trim() === "" ? undefined : form.prefix?.trim(),
      credential_ref: form.credential_ref === "" ? undefined : form.credential_ref,
    };
    if (validateDestination(input).length > 0) return;
    if (destination === undefined) {
      create.mutate(input);
    } else {
      update.mutate({ destinationId: destination.id, input });
    }
  }

  return (
    <Dialog
      open={open}
      title={destination === undefined ? "Add destination" : "Edit destination"}
      description="S3-compatible object storage. Credentials are referenced, never embedded."
      onClose={onClose}
    >
      <div className="flex flex-col gap-3">
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div>
            <label className="mb-1 block text-sm font-medium">Name</label>
            <Input value={form.name} onChange={(e) => patch({ name: e.target.value })} placeholder="Primary S3" />
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium">Bucket</label>
            <Input value={form.bucket} onChange={(e) => patch({ bucket: e.target.value })} placeholder="vaultix-backups" className="font-mono" />
          </div>
        </div>
        <div>
          <label className="mb-1 block text-sm font-medium">Endpoint (blank = AWS S3)</label>
          <Input
            value={form.endpoint ?? ""}
            onChange={(e) => patch({ endpoint: e.target.value })}
            placeholder="https://s3.example.com"
            className="font-mono"
          />
        </div>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div>
            <label className="mb-1 block text-sm font-medium">Region</label>
            <Input value={form.region ?? ""} onChange={(e) => patch({ region: e.target.value })} placeholder="us-east-1" className="font-mono" />
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium">Prefix</label>
            <Input value={form.prefix ?? ""} onChange={(e) => patch({ prefix: e.target.value })} placeholder="vaultix/" className="font-mono" />
          </div>
        </div>
        <div>
          <label className="mb-1 block text-sm font-medium">Credential profile</label>
          <Select value={form.credential_ref ?? ""} onChange={(e) => patch({ credential_ref: e.target.value })}>
            <option value="">(none)</option>
            {(credentials.data ?? []).map((c) => (
              <option key={c.id} value={c.key_ref !== "" ? c.key_ref : c.id}>
                {c.name} ({c.kind})
              </option>
            ))}
          </Select>
        </div>
        <div className="flex flex-wrap gap-4">
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={form.use_path_style}
              onChange={(e) => patch({ use_path_style: e.target.checked })}
            />
            Path-style URLs
          </label>
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={form.default} onChange={(e) => patch({ default: e.target.checked })} />
            Default destination
          </label>
        </div>
        {errors.length > 0 && (
          <ul className="flex flex-col gap-1 text-xs text-destructive">
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
            {busy ? "Saving…" : destination === undefined ? "Add destination" : "Save destination"}
          </Button>
        </div>
      </div>
    </Dialog>
  );
}

export function StoragePage() {
  const [dialog, setDialog] = useState<{ open: boolean; destination?: StorageDestination }>({ open: false });
  const [confirmDelete, setConfirmDelete] = useState<StorageDestination | undefined>(undefined);
  const [testResults, setTestResults] = useState<Record<string, TestConnectionResult>>({});
  const [testingId, setTestingId] = useState<ID | undefined>(undefined);

  const destinations = useStorageDestinations();
  const remove = useDeleteDestination();
  const test = useTestDestination();

  function runTest(destinationId: ID): void {
    setTestingId(destinationId);
    test.mutate(destinationId, {
      onSuccess: (res) => {
        setTestResults((prev) => ({ ...prev, [destinationId]: res }));
        setTestingId(undefined);
      },
      onError: () => setTestingId(undefined),
    });
  }

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Storage"
        description="Configure and test S3-compatible object storage destinations."
        action={
          <Button type="button" onClick={() => setDialog({ open: true })}>
            <Plus aria-hidden />
            Add destination
          </Button>
        }
      />
      <Card>
        <CardHeader>
          <CardTitle>Destinations</CardTitle>
        </CardHeader>
        <CardContent>
          {destinations.isLoading ? (
            <TableSkeleton rows={4} cols={4} />
          ) : destinations.data === undefined ? (
            <p className="text-sm text-destructive">Could not load destinations: {errorText(destinations.error)}</p>
          ) : destinations.data.length === 0 ? (
            <p className="text-sm text-gray-500">No destinations yet. Policies fall back to the default when unset.</p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm">
                <thead>
                  <tr className="border-b border-border text-xs text-gray-500">
                    <th className="py-2 pr-4 font-medium">Name</th>
                    <th className="py-2 pr-4 font-medium">Endpoint</th>
                    <th className="py-2 pr-4 font-medium">Bucket</th>
                    <th className="py-2 pr-4 font-medium">Connection</th>
                    <th className="py-2 pr-4 font-medium">
                      <span className="sr-only">Actions</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {destinations.data.map((d) => {
                    const result = testResults[d.id];
                    const testing = testingId === d.id;
                    return (
                      <tr key={d.id} className="border-b border-border">
                        <td className="py-2 pr-4">
                          <span className="font-medium">{d.name}</span>{" "}
                          {d.default && (
                            <span className="inline-flex items-center rounded-full border border-info/30 bg-info/10 px-2 py-0.5 text-xs font-medium text-info">
                              default
                            </span>
                          )}
                        </td>
                        <td className="py-2 pr-4 font-mono text-xs">{d.endpoint === "" ? "(AWS S3)" : d.endpoint}</td>
                        <td className="py-2 pr-4 font-mono text-xs">
                          {d.bucket}
                          {d.prefix !== "" ? `/${d.prefix}` : ""}
                        </td>
                        <td className="py-2 pr-4 text-xs">
                          {testing ? (
                            <span className="flex items-center gap-1 text-gray-500">
                              <Loader2 className="size-3 animate-spin" aria-hidden />
                              Testing…
                            </span>
                          ) : result === undefined ? (
                            <span className="text-gray-500">not tested</span>
                          ) : result.ok ? (
                            <span className="font-medium text-success" title={formatDateTime(result.checked_at)}>
                              OK
                            </span>
                          ) : (
                            <span className="font-medium text-destructive" title={result.error ?? "probe failed"}>
                              Failed{result.error !== undefined && result.error !== "" ? `: ${result.error}` : ""}
                            </span>
                          )}
                        </td>
                        <td className="py-2 pr-4">
                          <span className="flex flex-wrap gap-1">
                            <Button type="button" variant="ghost" size="sm" disabled={testing} onClick={() => runTest(d.id)}>
                              Test
                            </Button>
                            <Button
                              type="button"
                              variant="ghost"
                              size="sm"
                              onClick={() => setDialog({ open: true, destination: d })}
                            >
                              Edit
                            </Button>
                            <Button type="button" variant="ghost" size="sm" onClick={() => setConfirmDelete(d)}>
                              Delete
                            </Button>
                          </span>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}
          {test.isError && (
            <p className="mt-2 text-sm text-destructive">Test request failed: {errorText(test.error)}</p>
          )}
        </CardContent>
      </Card>

      <DestinationDialog
        open={dialog.open}
        destination={dialog.destination}
        onClose={() => setDialog({ open: false })}
      />

      <ConfirmDialog
        open={confirmDelete !== undefined}
        title="Delete destination?"
        body={`Policies pointing at “${confirmDelete?.name ?? ""}” will fail until they are re-pointed.`}
        confirmLabel="Delete destination"
        busy={remove.isPending}
        onConfirm={() => {
          if (confirmDelete !== undefined) {
            remove.mutate(confirmDelete.id, { onSuccess: () => setConfirmDelete(undefined) });
          }
        }}
        onClose={() => setConfirmDelete(undefined)}
      />
    </div>
  );
}
