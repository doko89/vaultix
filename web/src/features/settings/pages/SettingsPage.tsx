import { useEffect, useState } from "react";
import { Loader2, Plus } from "lucide-react";
import { errorText } from "@/shared/lib/api";
import { Button } from "@/shared/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/shared/ui/card";
import { ConfirmDialog } from "@/shared/ui/confirm-dialog";
import { Dialog } from "@/shared/ui/dialog";
import { Input } from "@/shared/ui/input";
import { PageHeader } from "@/shared/ui/page-header";
import { Select } from "@/shared/ui/select";
import { CardSkeleton } from "@/shared/ui/skeleton";
import {
  useCreateOidcProvider,
  useDeleteOidcProvider,
  useGlobalSettings,
  useOidcProviders,
  useTestOidcProvider,
  useUpdateGlobalSettings,
  useUpdateOidcProvider,
} from "../hooks";
import { validateProviderInput, validateSettings } from "../types";
import type { GlobalSettings, OidcProvider, OidcProviderInput } from "../types";

function ProviderDialog({
  open,
  provider,
  onClose,
}: {
  open: boolean;
  provider?: OidcProvider | undefined;
  onClose: () => void;
}) {
  const [name, setName] = useState("");
  const [issuer, setIssuer] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [discoveryUrl, setDiscoveryUrl] = useState("");
  const [clientId, setClientId] = useState("");
  const [clientSecret, setClientSecret] = useState("");
  const [defaultRole, setDefaultRole] = useState("");
  const [groupClaims, setGroupClaims] = useState("");
  const [jit, setJit] = useState(true);
  const [showErrors, setShowErrors] = useState(false);
  const create = useCreateOidcProvider(onClose);
  const update = useUpdateOidcProvider(onClose);

  useEffect(() => {
    if (!open) return;
    setShowErrors(false);
    setName(provider?.name ?? "");
    setIssuer(provider?.issuer ?? "");
    setEnabled(provider?.enabled ?? true);
    setDiscoveryUrl(provider?.discovery_url ?? "");
    setClientId(provider?.client_id ?? "");
    setClientSecret("");
    setDefaultRole(provider?.default_role ?? "");
    setGroupClaims((provider?.group_claim_names ?? []).join(", "));
    setJit(provider?.jit_provisioning ?? true);
  }, [open, provider]);

  const busy = create.isPending || update.isPending;
  const draft: OidcProviderInput = {
    name,
    issuer,
    enabled,
    client_id: clientId,
    jit_provisioning: jit,
  };
  const errors = showErrors ? validateProviderInput(draft) : [];

  function submit(): void {
    setShowErrors(true);
    const input: OidcProviderInput = {
      name: name.trim(),
      issuer: issuer.trim(),
      enabled,
      client_id: clientId.trim(),
      jit_provisioning: jit,
    };
    if (discoveryUrl.trim() !== "") input.discovery_url = discoveryUrl.trim();
    if (clientSecret !== "") input.client_secret = clientSecret;
    if (defaultRole !== "") input.default_role = defaultRole;
    const claims = groupClaims.split(",").map((s) => s.trim()).filter((s) => s !== "");
    if (claims.length > 0) input.group_claim_names = claims;
    if (validateProviderInput(input).length > 0) return;
    if (provider === undefined) {
      create.mutate(input);
    } else {
      update.mutate({ providerId: provider.id, input });
    }
  }

  return (
    <Dialog
      open={open}
      title={provider === undefined ? "Add OIDC provider" : "Edit OIDC provider"}
      description="Client secrets are write-only and stored encrypted; reads never return them."
      onClose={onClose}
      wide
    >
      <div className="flex flex-col gap-3">
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div>
            <label className="mb-1 block text-sm font-medium">Name (login button label)</label>
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="Keycloak" />
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium">Issuer</label>
            <Input
              value={issuer}
              onChange={(e) => setIssuer(e.target.value)}
              placeholder="https://idp.example.com/realms/main"
              className="font-mono"
            />
          </div>
        </div>
        <div>
          <label className="mb-1 block text-sm font-medium">Discovery URL override (optional)</label>
          <Input
            value={discoveryUrl}
            onChange={(e) => setDiscoveryUrl(e.target.value)}
            placeholder="Blank = issuer + /.well-known/openid-configuration"
            className="font-mono"
          />
        </div>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div>
            <label className="mb-1 block text-sm font-medium">Client ID</label>
            <Input value={clientId} onChange={(e) => setClientId(e.target.value)} autoComplete="off" className="font-mono" />
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium">
              Client secret{provider !== undefined ? " (blank keeps current)" : ""}
            </label>
            <Input
              type="password"
              value={clientSecret}
              onChange={(e) => setClientSecret(e.target.value)}
              autoComplete="new-password"
            />
          </div>
        </div>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div>
            <label className="mb-1 block text-sm font-medium">Group claim names (comma-separated)</label>
            <Input
              value={groupClaims}
              onChange={(e) => setGroupClaims(e.target.value)}
              placeholder="groups, roles"
              className="font-mono"
            />
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium">Default role (blank denies)</label>
            <Select value={defaultRole} onChange={(e) => setDefaultRole(e.target.value)}>
              <option value="">(deny when unmapped)</option>
              <option value="administrator">administrator</option>
              <option value="operator">operator</option>
              <option value="readonly">readonly</option>
            </Select>
          </div>
        </div>
        <div className="flex flex-wrap gap-4">
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
            Enabled
          </label>
          <label className="flex items-center gap-2 text-sm" title="Create the user on first login">
            <input type="checkbox" checked={jit} onChange={(e) => setJit(e.target.checked)} />
            JIT provisioning
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
            {busy ? "Saving…" : provider === undefined ? "Add provider" : "Save provider"}
          </Button>
        </div>
      </div>
    </Dialog>
  );
}

function GeneralSettingsCard() {
  const settings = useGlobalSettings();
  const save = useUpdateGlobalSettings();
  const [form, setForm] = useState<GlobalSettings | undefined>(undefined);
  const [showErrors, setShowErrors] = useState(false);

  useEffect(() => {
    if (settings.data !== undefined && form === undefined) {
      setForm(settings.data);
    }
  }, [settings.data, form]);

  if (settings.isLoading || form === undefined) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>General</CardTitle>
        </CardHeader>
        <CardContent>
          <CardSkeleton />
        </CardContent>
      </Card>
    );
  }
  if (settings.data === undefined) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>General</CardTitle>
        </CardHeader>
        <CardContent>
          <p className="text-sm text-destructive">Could not load settings: {errorText(settings.error)}</p>
        </CardContent>
      </Card>
    );
  }

  const errors = showErrors ? validateSettings(form) : [];

  function patch(next: Partial<GlobalSettings>): void {
    setForm((prev) => (prev === undefined ? prev : { ...prev, ...next }));
  }

  function submit(): void {
    setShowErrors(true);
    if (form === undefined) return;
    if (validateSettings(form).length > 0) return;
    save.mutate(form);
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>General</CardTitle>
      </CardHeader>
      <CardContent>
        <div className="grid max-w-2xl grid-cols-1 gap-3 sm:grid-cols-2">
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={form.local_login_enabled}
              onChange={(e) => patch({ local_login_enabled: e.target.checked })}
            />
            Local login enabled
          </label>
          <div>
            <label className="mb-1 block text-sm font-medium">Default role</label>
            <Select value={form.default_role} onChange={(e) => patch({ default_role: e.target.value })}>
              <option value="">(deny when unmapped)</option>
              <option value="administrator">administrator</option>
              <option value="operator">operator</option>
              <option value="readonly">readonly</option>
            </Select>
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium">Session lifetime (minutes)</label>
            <Input
              inputMode="numeric"
              value={String(form.session_lifetime_minutes)}
              onChange={(e) => patch({ session_lifetime_minutes: Number(e.target.value) || 0 })}
            />
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium">Session idle timeout (minutes)</label>
            <Input
              inputMode="numeric"
              value={String(form.session_idle_minutes)}
              onChange={(e) => patch({ session_idle_minutes: Number(e.target.value) || 0 })}
            />
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium">Heartbeat timeout (seconds)</label>
            <Input
              inputMode="numeric"
              value={String(form.heartbeat_timeout_seconds)}
              onChange={(e) => patch({ heartbeat_timeout_seconds: Number(e.target.value) || 0 })}
            />
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium">Global concurrency (0 = unset)</label>
            <Input
              inputMode="numeric"
              value={String(form.global_concurrency)}
              onChange={(e) => patch({ global_concurrency: Number(e.target.value) || 0 })}
            />
          </div>
        </div>
        {errors.length > 0 && (
          <ul className="mt-3 flex flex-col gap-1 text-xs text-destructive">
            {errors.map((e, i) => (
              <li key={i}>{e}</li>
            ))}
          </ul>
        )}
        <div className="mt-4">
          <Button type="button" onClick={submit} disabled={save.isPending}>
            {save.isPending ? "Saving…" : "Save changes"}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

export function SettingsPage() {
  const [dialog, setDialog] = useState<{ open: boolean; provider?: OidcProvider }>({ open: false });
  const [confirmDelete, setConfirmDelete] = useState<OidcProvider | undefined>(undefined);
  const [testingId, setTestingId] = useState<string | undefined>(undefined);
  const providers = useOidcProviders();
  const remove = useDeleteOidcProvider();
  const test = useTestOidcProvider();

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Settings"
        description="Global configuration, OIDC providers, and system health."
        action={
          <Button type="button" onClick={() => setDialog({ open: true })}>
            <Plus aria-hidden />
            Add OIDC provider
          </Button>
        }
      />

      <GeneralSettingsCard />

      <Card>
        <CardHeader>
          <CardTitle>OIDC providers</CardTitle>
        </CardHeader>
        <CardContent>
          {providers.isLoading ? (
            <CardSkeleton />
          ) : providers.data === undefined ? (
            <p className="text-sm text-destructive">Could not load providers: {errorText(providers.error)}</p>
          ) : providers.data.items.length === 0 ? (
            <p className="text-sm text-gray-500">No OIDC providers configured. Local login remains available.</p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm">
                <thead>
                  <tr className="border-b border-border text-xs text-gray-500">
                    <th className="py-2 pr-4 font-medium">Name</th>
                    <th className="py-2 pr-4 font-medium">Issuer</th>
                    <th className="py-2 pr-4 font-medium">Client ID</th>
                    <th className="py-2 pr-4 font-medium">State</th>
                    <th className="py-2 pr-4 font-medium">
                      <span className="sr-only">Actions</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {providers.data.items.map((p) => (
                    <tr key={p.id} className="border-b border-border">
                      <td className="py-2 pr-4 font-medium">{p.name}</td>
                      <td className="max-w-[15rem] truncate py-2 pr-4 font-mono text-xs" title={p.issuer}>
                        {p.issuer}
                      </td>
                      <td className="max-w-[10rem] truncate py-2 pr-4 font-mono text-xs">{p.client_id}</td>
                      <td className="py-2 pr-4">
                        {p.enabled ? (
                          <span className="inline-flex items-center rounded-full border border-success/30 bg-success/10 px-2.5 py-0.5 text-xs font-medium text-success">
                            Enabled
                          </span>
                        ) : (
                          <span className="inline-flex items-center rounded-full border border-border bg-surface px-2.5 py-0.5 text-xs font-medium text-gray-500">
                            Disabled
                          </span>
                        )}
                      </td>
                      <td className="py-2 pr-4">
                        <span className="flex flex-wrap gap-1">
                          <Button
                            type="button"
                            variant="ghost"
                            size="sm"
                            disabled={testingId === p.id}
                            onClick={() => {
                              setTestingId(p.id);
                              test.mutate(p.id, { onSettled: () => setTestingId(undefined) });
                            }}
                          >
                            {testingId === p.id && <Loader2 className="animate-spin" aria-hidden />}
                            Test
                          </Button>
                          <Button
                            type="button"
                            variant="ghost"
                            size="sm"
                            onClick={() => setDialog({ open: true, provider: p })}
                          >
                            Edit
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

      <ProviderDialog open={dialog.open} provider={dialog.provider} onClose={() => setDialog({ open: false })} />

      <ConfirmDialog
        open={confirmDelete !== undefined}
        title="Delete provider?"
        body={`“${confirmDelete?.name ?? ""}” disappears from the login page. Linked identities stay but cannot start new logins.`}
        confirmLabel="Delete provider"
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
