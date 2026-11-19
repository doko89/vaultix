import { useEffect, useState } from "react";
import { Plus } from "lucide-react";
import type { ID } from "@/shared/types";
import { errorText } from "@/shared/lib/api";
import { useDebouncedValue } from "@/shared/hooks/use-debounce";
import { Button } from "@/shared/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/shared/ui/card";
import { ConfirmDialog } from "@/shared/ui/confirm-dialog";
import { Dialog } from "@/shared/ui/dialog";
import { Input } from "@/shared/ui/input";
import { PageHeader } from "@/shared/ui/page-header";
import { Pagination } from "@/shared/ui/pagination";
import { Select } from "@/shared/ui/select";
import { TableSkeleton } from "@/shared/ui/skeleton";
import {
  useChangeOwnPassword,
  useCreateUser,
  useCurrentSession,
  useGrantRole,
  useIdentities,
  useLinkIdentity,
  useRevokeRole,
  useRoles,
  useSetIdentityDisabled,
  useSetUserDisabled,
  useSetUserPassword,
  useUnlinkIdentity,
  useUpdateUser,
  useUser,
  useUserRoles,
  useUsers,
} from "../hooks";
import { sessionIsAdmin } from "../types";
import type { CreateUserInput, ExternalIdentity, LinkIdentityInput, UpdateUserInput, User } from "../types";

const PAGE_SIZE = 25;

function CreateUserDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [username, setUsername] = useState("");
  const [email, setEmail] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [password, setPassword] = useState("");
  const [roles, setRoles] = useState<string[]>(["readonly"]);
  const [showErrors, setShowErrors] = useState(false);
  const allRoles = useRoles();
  const create = useCreateUser(onClose);

  useEffect(() => {
    if (open) {
      setUsername("");
      setEmail("");
      setDisplayName("");
      setPassword("");
      setRoles(["readonly"]);
      setShowErrors(false);
    }
  }, [open]);

  const errors: string[] = [];
  if (showErrors) {
    if (username.trim() === "") errors.push("Username is required.");
    if (!email.includes("@")) errors.push("A valid email is required.");
    if (password !== "" && password.length < 8) errors.push("Password must be at least 8 characters (or blank for OIDC-only).");
  }

  function toggleRole(role: string, checked: boolean): void {
    setRoles((prev) => (checked ? [...prev, role] : prev.filter((r) => r !== role)));
  }

  function submit(): void {
    setShowErrors(true);
    const input: CreateUserInput = {
      username: username.trim(),
      email: email.trim(),
      roles,
    };
    if (displayName.trim() !== "") input.display_name = displayName.trim();
    if (password !== "") input.password = password;
    if (input.username === "" || !input.email.includes("@")) return;
    if (input.password !== undefined && input.password.length < 8) return;
    create.mutate(input);
  }

  return (
    <Dialog open={open} title="Add user" description="Password may be blank for OIDC-only users." onClose={onClose}>
      <div className="flex flex-col gap-3">
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div>
            <label className="mb-1 block text-sm font-medium">Username</label>
            <Input value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="off" />
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium">Email</label>
            <Input value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="off" />
          </div>
        </div>
        <div>
          <label className="mb-1 block text-sm font-medium">Display name</label>
          <Input value={displayName} onChange={(e) => setDisplayName(e.target.value)} autoComplete="off" />
        </div>
        <div>
          <label className="mb-1 block text-sm font-medium">Password (min 8, or blank)</label>
          <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" />
        </div>
        <div>
          <span className="mb-1 block text-sm font-medium">Roles</span>
          <div className="flex flex-wrap gap-3">
            {(allRoles.data?.items ?? []).map((r) => (
              <label key={r.id} className="flex items-center gap-2 text-sm" title={r.description}>
                <input type="checkbox" checked={roles.includes(r.name)} onChange={(e) => toggleRole(r.name, e.target.checked)} />
                {r.name}
              </label>
            ))}
          </div>
        </div>
        {errors.length > 0 && (
          <ul className="flex flex-col gap-1 text-xs text-destructive">
            {errors.map((e, i) => (
              <li key={i}>{e}</li>
            ))}
          </ul>
        )}
        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" onClick={onClose} disabled={create.isPending}>
            Cancel
          </Button>
          <Button type="button" onClick={submit} disabled={create.isPending}>
            {create.isPending ? "Creating…" : "Add user"}
          </Button>
        </div>
      </div>
    </Dialog>
  );
}

function LinkIdentityDialog({ userId, open, onClose }: { userId: ID; open: boolean; onClose: () => void }) {
  const [issuer, setIssuer] = useState("");
  const [subject, setSubject] = useState("");
  const [email, setEmail] = useState("");
  const [showErrors, setShowErrors] = useState(false);
  const link = useLinkIdentity(onClose);

  useEffect(() => {
    if (open) {
      setIssuer("");
      setSubject("");
      setEmail("");
      setShowErrors(false);
    }
  }, [open]);

  function submit(): void {
    setShowErrors(true);
    if (issuer.trim() === "" || subject.trim() === "") return;
    const input: LinkIdentityInput = { issuer: issuer.trim(), subject: subject.trim() };
    if (email.trim() !== "") input.email = email.trim();
    link.mutate({ userId, input });
  }

  return (
    <Dialog open={open} title="Link OIDC identity" description="Binds a provider (issuer, subject) pair to this user." onClose={onClose}>
      <div className="flex flex-col gap-3">
        <div>
          <label className="mb-1 block text-sm font-medium">Issuer</label>
          <Input value={issuer} onChange={(e) => setIssuer(e.target.value)} placeholder="https://idp.example.com" className="font-mono" />
          {showErrors && issuer.trim() === "" && <p className="mt-1 text-xs text-destructive">Issuer is required.</p>}
        </div>
        <div>
          <label className="mb-1 block text-sm font-medium">Subject (sub)</label>
          <Input value={subject} onChange={(e) => setSubject(e.target.value)} className="font-mono" />
          {showErrors && subject.trim() === "" && <p className="mt-1 text-xs text-destructive">Subject is required.</p>}
        </div>
        <div>
          <label className="mb-1 block text-sm font-medium">Email (optional)</label>
          <Input value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="off" />
        </div>
        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" onClick={onClose} disabled={link.isPending}>
            Cancel
          </Button>
          <Button type="button" onClick={submit} disabled={link.isPending}>
            {link.isPending ? "Linking…" : "Link identity"}
          </Button>
        </div>
      </div>
    </Dialog>
  );
}

function SetPasswordDialog({ user, open, onClose }: { user: User | undefined; open: boolean; onClose: () => void }) {
  const [password, setPassword] = useState("");
  const set = useSetUserPassword(onClose);

  useEffect(() => {
    if (open) setPassword("");
  }, [open]);

  return (
    <Dialog open={open} title={`Set password for ${user?.username ?? "user"}`} onClose={onClose}>
      <div className="flex flex-col gap-3">
        <div>
          <label className="mb-1 block text-sm font-medium">New password (min 8)</label>
          <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" />
        </div>
        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" onClick={onClose} disabled={set.isPending}>
            Cancel
          </Button>
          <Button
            type="button"
            disabled={set.isPending || password.length < 8 || user === undefined}
            onClick={() => {
              if (user !== undefined) set.mutate({ userId: user.id, password });
            }}
          >
            {set.isPending ? "Saving…" : "Set password"}
          </Button>
        </div>
      </div>
    </Dialog>
  );
}

function IdentityRow({ userId, identity }: { userId: ID; identity: ExternalIdentity }) {
  const setDisabled = useSetIdentityDisabled();
  const unlink = useUnlinkIdentity();
  const [confirmUnlink, setConfirmUnlink] = useState(false);

  return (
    <li className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-border p-2 text-sm">
      <span className="min-w-0">
        <span className="block truncate font-mono text-xs">
          {identity.issuer} · {identity.subject}
        </span>
        <span className="text-xs text-gray-500">
          {identity.email !== "" ? `${identity.email} · ` : ""}
          {identity.disabled ? "disabled" : "active"}
        </span>
      </span>
      <span className="flex gap-1">
        <Button
          type="button"
          variant="ghost"
          size="sm"
          disabled={setDisabled.isPending}
          onClick={() => setDisabled.mutate({ userId, identityId: identity.id, disabled: !identity.disabled })}
        >
          {identity.disabled ? "Enable" : "Disable"}
        </Button>
        <Button type="button" variant="ghost" size="sm" onClick={() => setConfirmUnlink(true)}>
          Unlink
        </Button>
      </span>
      <ConfirmDialog
        open={confirmUnlink}
        title="Unlink identity?"
        body="The user loses this OIDC login path. Their local account and roles stay."
        confirmLabel="Unlink identity"
        busy={unlink.isPending}
        onConfirm={() => {
          unlink.mutate({ userId, identityId: identity.id }, { onSuccess: () => setConfirmUnlink(false) });
        }}
        onClose={() => setConfirmUnlink(false)}
      />
    </li>
  );
}

function UserDetail({ userId, onClose }: { userId: ID; onClose: () => void }) {
  const detail = useUser(userId);
  const userRoles = useUserRoles(userId);
  const identities = useIdentities(userId);
  const allRoles = useRoles();
  const grant = useGrantRole();
  const revoke = useRevokeRole();
  const setDisabled = useSetUserDisabled();
  const update = useUpdateUser(() => undefined);

  const [email, setEmail] = useState<string | undefined>(undefined);
  const [displayName, setDisplayName] = useState<string | undefined>(undefined);
  const [grantRole, setGrantRole] = useState("");
  const [linkOpen, setLinkOpen] = useState(false);
  const [passwordOpen, setPasswordOpen] = useState(false);
  const [confirmDisable, setConfirmDisable] = useState(false);

  const user = detail.data;
  const held = new Set((userRoles.data?.items ?? []).map((r) => r.name));
  const grantable = (allRoles.data?.items ?? []).filter((r) => !held.has(r.name));

  function saveProfile(): void {
    if (user === undefined) return;
    const input: UpdateUserInput = {};
    if (email !== undefined) input.email = email;
    if (displayName !== undefined) input.display_name = displayName;
    update.mutate({ userId, input });
  }

  return (
    <Card>
      <CardHeader>
        <div className="flex items-start justify-between gap-4">
          <CardTitle>{user?.username ?? "User detail"}</CardTitle>
          <Button type="button" variant="ghost" size="sm" onClick={onClose}>
            Close
          </Button>
        </div>
      </CardHeader>
      <CardContent>
        {user === undefined ? (
          <p className="text-sm text-gray-500">{detail.isLoading ? "Loading…" : "Could not load this user."}</p>
        ) : (
          <div className="flex flex-col gap-5">
            <div className="flex flex-wrap items-center gap-2">
              {user.disabled ? (
                <span className="inline-flex items-center rounded-full border border-destructive/30 bg-destructive/10 px-2.5 py-0.5 text-xs font-medium text-destructive">
                  Disabled
                </span>
              ) : (
                <span className="inline-flex items-center rounded-full border border-success/30 bg-success/10 px-2.5 py-0.5 text-xs font-medium text-success">
                  Active
                </span>
              )}
              {user.local_login_disabled && (
                <span className="inline-flex items-center rounded-full border border-border bg-surface px-2.5 py-0.5 text-xs font-medium text-gray-500">
                  SSO-only
                </span>
              )}
            </div>

            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <div>
                <label className="mb-1 block text-sm font-medium">Email</label>
                <Input value={email ?? user.email} onChange={(e) => setEmail(e.target.value)} />
              </div>
              <div>
                <label className="mb-1 block text-sm font-medium">Display name</label>
                <Input value={displayName ?? user.display_name} onChange={(e) => setDisplayName(e.target.value)} />
              </div>
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <Button type="button" variant="outline" size="sm" disabled={update.isPending} onClick={saveProfile}>
                Save profile
              </Button>
              <label className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={user.local_login_disabled}
                  onChange={(e) => update.mutate({ userId, input: { local_login_disabled: e.target.checked } })}
                />
                Force SSO-only login
              </label>
            </div>

            <div>
              <h3 className="mb-1 text-sm font-semibold">Roles</h3>
              <div className="flex flex-wrap gap-2">
                {(userRoles.data?.items ?? []).map((r) => (
                  <span
                    key={r.id}
                    className="inline-flex items-center gap-1 rounded-full border border-border bg-surface px-2.5 py-0.5 text-xs"
                  >
                    {r.name}
                    <button
                      type="button"
                      className="font-medium text-destructive hover:underline"
                      aria-label={`Revoke ${r.name}`}
                      onClick={() => revoke.mutate({ userId, role: r.name })}
                    >
                      ×
                    </button>
                  </span>
                ))}
                {(userRoles.data?.items ?? []).length === 0 && (
                  <span className="text-sm text-gray-500">No roles — the account cannot log in.</span>
                )}
              </div>
              {grantable.length > 0 && (
                <div className="mt-2 flex gap-2">
                  <Select value={grantRole} onChange={(e) => setGrantRole(e.target.value)} className="w-auto" aria-label="Role to grant">
                    <option value="">Grant a role…</option>
                    {grantable.map((r) => (
                      <option key={r.id} value={r.name}>
                        {r.name}
                      </option>
                    ))}
                  </Select>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    disabled={grantRole === "" || grant.isPending}
                    onClick={() => {
                      if (grantRole !== "") {
                        grant.mutate({ userId, role: grantRole }, { onSuccess: () => setGrantRole("") });
                      }
                    }}
                  >
                    Grant
                  </Button>
                </div>
              )}
            </div>

            <div>
              <div className="mb-1 flex items-center justify-between">
                <h3 className="text-sm font-semibold">OIDC identities</h3>
                <Button type="button" variant="outline" size="sm" onClick={() => setLinkOpen(true)}>
                  <Plus aria-hidden />
                  Link
                </Button>
              </div>
              {identities.data === undefined ? (
                <p className="text-sm text-gray-500">{identities.isLoading ? "Loading…" : "Could not load identities."}</p>
              ) : identities.data.items.length === 0 ? (
                <p className="text-sm text-gray-500">No linked identities.</p>
              ) : (
                <ul className="flex flex-col gap-2">
                  {identities.data.items.map((identity) => (
                    <IdentityRow key={identity.id} userId={userId} identity={identity} />
                  ))}
                </ul>
              )}
            </div>

            <div className="flex flex-wrap gap-2">
              <Button type="button" variant="outline" size="sm" onClick={() => setPasswordOpen(true)}>
                Set password
              </Button>
              <Button
                type="button"
                variant={user.disabled ? "outline" : "destructive"}
                size="sm"
                onClick={() => {
                  if (user.disabled) {
                    setDisabled.mutate({ userId, disabled: false });
                  } else {
                    setConfirmDisable(true);
                  }
                }}
              >
                {user.disabled ? "Enable user" : "Disable user"}
              </Button>
            </div>
          </div>
        )}
      </CardContent>

      <LinkIdentityDialog userId={userId} open={linkOpen} onClose={() => setLinkOpen(false)} />
      <SetPasswordDialog user={user} open={passwordOpen} onClose={() => setPasswordOpen(false)} />
      <ConfirmDialog
        open={confirmDisable}
        title="Disable user?"
        body={`${user?.username ?? "This user"} will be unable to log in until re-enabled.`}
        confirmLabel="Disable user"
        busy={setDisabled.isPending}
        onConfirm={() => {
          setDisabled.mutate({ userId, disabled: true }, { onSuccess: () => setConfirmDisable(false) });
        }}
        onClose={() => setConfirmDisable(false)}
      />
    </Card>
  );
}

function OwnPasswordCard() {
  const [oldPassword, setOldPassword] = useState("");
  const [next1, setNext1] = useState("");
  const [next2, setNext2] = useState("");
  const change = useChangeOwnPassword();

  const mismatch = next1 !== "" && next2 !== "" && next1 !== next2;
  const valid = oldPassword !== "" && next1.length >= 8 && !mismatch;

  return (
    <Card>
      <CardHeader>
        <CardTitle>Your password</CardTitle>
      </CardHeader>
      <CardContent>
        <div className="grid max-w-lg grid-cols-1 gap-3">
          <div>
            <label className="mb-1 block text-sm font-medium">Current password</label>
            <Input type="password" value={oldPassword} onChange={(e) => setOldPassword(e.target.value)} autoComplete="current-password" />
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium">New password (min 8)</label>
            <Input type="password" value={next1} onChange={(e) => setNext1(e.target.value)} autoComplete="new-password" />
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium">Repeat new password</label>
            <Input type="password" value={next2} onChange={(e) => setNext2(e.target.value)} autoComplete="new-password" />
            {mismatch && <p className="mt-1 text-xs text-destructive">Passwords do not match.</p>}
          </div>
          <div>
            <Button
              type="button"
              disabled={!valid || change.isPending}
              onClick={() => change.mutate({ oldPassword, newPassword: next1 })}
            >
              {change.isPending ? "Changing…" : "Change password"}
            </Button>
            <p className="mt-1 text-xs text-gray-500">All sessions are revoked — you will log in again.</p>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}

export function UsersPage() {
  const [search, setSearch] = useState("");
  const [offset, setOffset] = useState(0);
  const [selected, setSelected] = useState<ID | undefined>(undefined);
  const [createOpen, setCreateOpen] = useState(false);
  const q = useDebouncedValue(search.trim());

  const session = useCurrentSession();
  const admin = sessionIsAdmin(session.data);
  const users = useUsers({ q: q === "" ? undefined : q, limit: PAGE_SIZE, offset });

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Users & Roles"
        description="Manage access, permissions, OIDC links, and group-to-role mapping."
        action={
          admin ? (
            <Button type="button" onClick={() => setCreateOpen(true)}>
              <Plus aria-hidden />
              Add user
            </Button>
          ) : undefined
        }
      />

      {!session.isLoading && !admin ? (
        <Card>
          <CardContent className="pt-6">
            <p className="text-sm text-gray-500">
              User administration needs the administrator role. You can still change your own password below.
            </p>
          </CardContent>
        </Card>
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-3">
            <Input
              type="search"
              placeholder="Search users…"
              aria-label="Search users"
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
              <CardTitle>All users</CardTitle>
            </CardHeader>
            <CardContent>
              {users.isLoading ? (
                <TableSkeleton rows={5} cols={4} />
              ) : users.data === undefined ? (
                <p className="text-sm text-destructive">Could not load users: {errorText(users.error)}</p>
              ) : users.data.items.length === 0 ? (
                <p className="text-sm text-gray-500">No users found.</p>
              ) : (
                <div className="overflow-x-auto">
                  <table className="w-full text-left text-sm">
                    <thead>
                      <tr className="border-b border-border text-xs text-gray-500">
                        <th className="py-2 pr-4 font-medium">Username</th>
                        <th className="py-2 pr-4 font-medium">Email</th>
                        <th className="py-2 pr-4 font-medium">Display name</th>
                        <th className="py-2 pr-4 font-medium">State</th>
                      </tr>
                    </thead>
                    <tbody>
                      {users.data.items.map((u) => (
                        <tr
                          key={u.id}
                          className={
                            selected === u.id
                              ? "cursor-pointer border-b border-border bg-primary/5"
                              : "cursor-pointer border-b border-border hover:bg-surface"
                          }
                          onClick={() => setSelected((prev) => (prev === u.id ? undefined : u.id))}
                        >
                          <td className="py-2 pr-4 font-medium">{u.username}</td>
                          <td className="py-2 pr-4">{u.email}</td>
                          <td className="py-2 pr-4">{u.display_name === "" ? "—" : u.display_name}</td>
                          <td className="py-2 pr-4">
                            {u.disabled ? (
                              <span className="inline-flex items-center rounded-full border border-destructive/30 bg-destructive/10 px-2.5 py-0.5 text-xs font-medium text-destructive">
                                Disabled
                              </span>
                            ) : (
                              <span className="inline-flex items-center rounded-full border border-success/30 bg-success/10 px-2.5 py-0.5 text-xs font-medium text-success">
                                Active
                              </span>
                            )}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
              {users.data !== undefined && users.data.total > 0 && (
                <Pagination total={users.data.total} limit={PAGE_SIZE} offset={offset} onChange={setOffset} />
              )}
            </CardContent>
          </Card>

          {selected !== undefined && <UserDetail userId={selected} onClose={() => setSelected(undefined)} />}

          <CreateUserDialog open={createOpen} onClose={() => setCreateOpen(false)} />
        </>
      )}

      <OwnPasswordCard />
    </div>
  );
}
