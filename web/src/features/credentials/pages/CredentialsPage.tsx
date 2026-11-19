import { useEffect, useState } from "react";
import { Plus } from "lucide-react";
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
import { Textarea } from "@/shared/ui/textarea";
import {
  useCreateCredentialProfile,
  useCredentialProfiles,
  useDeleteCredentialProfile,
  useUpdateCredentialProfile,
} from "../hooks";
import { SECRET_KINDS, validateCredential } from "../types";
import type { CredentialInput, CredentialProfile, SecretKind } from "../types";

// Secret values use password inputs and are write-only: the form never
// renders stored plaintext (DESIGN section 7).

function CredentialDialog({
  open,
  profile,
  onClose,
}: {
  open: boolean;
  profile?: CredentialProfile | undefined;
  onClose: () => void;
}) {
  const [name, setName] = useState("");
  const [kind, setKind] = useState<SecretKind>("database");
  const [plaintext, setPlaintext] = useState("");
  const [showErrors, setShowErrors] = useState(false);
  const create = useCreateCredentialProfile(onClose);
  const update = useUpdateCredentialProfile(onClose);

  useEffect(() => {
    if (!open) return;
    setShowErrors(false);
    setName(profile?.name ?? "");
    setKind((profile?.kind as SecretKind) ?? "database");
    setPlaintext("");
  }, [open, profile]);

  const editing = profile !== undefined;
  const busy = create.isPending || update.isPending;
  const errors = showErrors ? validateCredential({ name, kind, plaintext }) : [];

  function submit(): void {
    setShowErrors(true);
    const input: CredentialInput = { name: name.trim(), kind, plaintext };
    if (validateCredential(input).length > 0) return;
    if (profile === undefined) {
      create.mutate(input);
    } else {
      update.mutate({ profileId: profile.id, input });
    }
  }

  return (
    <Dialog
      open={open}
      title={editing ? "Rotate profile" : "Add credential"}
      description={
        editing
          ? "Saving replaces the stored secret. The old value is unrecoverable."
          : "Binary material must be base64-encoded first; storage credentials use JSON with access_key_id and secret_access_key."
      }
      onClose={onClose}
    >
      <div className="flex flex-col gap-3">
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div>
            <label className="mb-1 block text-sm font-medium">Name</label>
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="billing-db" />
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium">Kind</label>
            <Select value={kind} onChange={(e) => setKind(e.target.value as SecretKind)}>
              {SECRET_KINDS.map((k) => (
                <option key={k} value={k}>
                  {k}
                </option>
              ))}
            </Select>
          </div>
        </div>
        <div>
          <label className="mb-1 block text-sm font-medium">
            {editing ? "New secret value" : "Secret value"}
          </label>
          <Textarea
            value={plaintext}
            onChange={(e) => setPlaintext(e.target.value)}
            placeholder="Write-only — never displayed again"
            autoComplete="new-password"
            className="font-mono"
          />
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
            {busy ? "Saving…" : editing ? "Rotate secret" : "Add credential"}
          </Button>
        </div>
      </div>
    </Dialog>
  );
}

export function CredentialsPage() {
  const [dialog, setDialog] = useState<{ open: boolean; profile?: CredentialProfile }>({ open: false });
  const [confirmDelete, setConfirmDelete] = useState<CredentialProfile | undefined>(undefined);
  const profiles = useCredentialProfiles();
  const remove = useDeleteCredentialProfile();

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Credentials"
        description="Manage protected database and service credential profiles."
        action={
          <Button type="button" onClick={() => setDialog({ open: true })}>
            <Plus aria-hidden />
            Add credential
          </Button>
        }
      />
      <Card>
        <CardHeader>
          <CardTitle>Profiles</CardTitle>
        </CardHeader>
        <CardContent>
          {profiles.isLoading ? (
            <TableSkeleton rows={4} cols={4} />
          ) : profiles.data === undefined ? (
            <p className="text-sm text-destructive">Could not load profiles: {errorText(profiles.error)}</p>
          ) : profiles.data.length === 0 ? (
            <p className="text-sm text-gray-500">No credential profiles yet.</p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm">
                <thead>
                  <tr className="border-b border-border text-xs text-gray-500">
                    <th className="py-2 pr-4 font-medium">Name</th>
                    <th className="py-2 pr-4 font-medium">Kind</th>
                    <th className="py-2 pr-4 font-medium">Key ref</th>
                    <th className="py-2 pr-4 font-medium">Updated</th>
                    <th className="py-2 pr-4 font-medium">
                      <span className="sr-only">Actions</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {profiles.data.map((p) => (
                    <tr key={p.id} className="border-b border-border">
                      <td className="py-2 pr-4 font-medium">{p.name}</td>
                      <td className="py-2 pr-4">
                        <span className="inline-flex items-center rounded-full border border-border bg-surface px-2.5 py-0.5 text-xs">
                          {p.kind}
                        </span>
                      </td>
                      <td className="py-2 pr-4 font-mono text-xs">{p.key_ref}</td>
                      <td className="py-2 pr-4 text-xs">{formatDateTime(p.updated_at)}</td>
                      <td className="py-2 pr-4">
                        <span className="flex flex-wrap gap-1">
                          <Button
                            type="button"
                            variant="ghost"
                            size="sm"
                            onClick={() => setDialog({ open: true, profile: p })}
                          >
                            Rotate
                          </Button>
                          <Button type="button" variant="ghost" size="sm" onClick={() => setConfirmDelete(p)}>
                            Delete
                          </Button>
                        </span>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>

      <CredentialDialog
        open={dialog.open}
        profile={dialog.profile}
        onClose={() => setDialog({ open: false })}
      />

      <ConfirmDialog
        open={confirmDelete !== undefined}
        title="Delete profile?"
        body={`Policies and destinations referencing “${confirmDelete?.name ?? ""}” will fail until they are re-pointed.`}
        confirmLabel="Delete profile"
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
