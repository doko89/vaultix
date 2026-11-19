import { api, qs } from "@/shared/lib/api";
import type { ID } from "@/shared/types";
import type {
  CreateUserInput,
  ExternalIdentity,
  LinkIdentityInput,
  Role,
  UpdateUserInput,
  User,
  UserList,
  UserSession,
} from "./types";

export function listUsers(
  opts: { q?: string | undefined; limit?: number | undefined; offset?: number | undefined } = {},
): Promise<UserList> {
  return api.get<UserList>(`/auth/users${qs({ q: opts.q, limit: opts.limit, offset: opts.offset })}`);
}

export function getUser(userId: ID): Promise<User> {
  return api.get<User>(`/auth/users/${encodeURIComponent(userId)}`);
}

export function createUser(input: CreateUserInput): Promise<User> {
  return api.post<User>("/auth/users", input);
}

export function updateUser(userId: ID, input: UpdateUserInput): Promise<User> {
  return api.patch<User>(`/auth/users/${encodeURIComponent(userId)}`, input);
}

export function setUserDisabled(userId: ID, disabled: boolean): Promise<void> {
  return api.post<void>(`/auth/users/${encodeURIComponent(userId)}/${disabled ? "disable" : "enable"}`);
}

export function setUserPassword(userId: ID, password: string): Promise<void> {
  return api.post<void>(`/auth/users/${encodeURIComponent(userId)}/password`, { password });
}

export function changeOwnPassword(oldPassword: string, newPassword: string): Promise<void> {
  // The server revokes all sessions on success: the UI must re-login after.
  return api.post<void>("/auth/password/change", { old_password: oldPassword, new_password: newPassword });
}

export function listRoles(): Promise<{ items: Role[] }> {
  return api.get<{ items: Role[] }>("/auth/roles");
}

export function listUserRoles(userId: ID): Promise<{ items: Role[] }> {
  return api.get<{ items: Role[] }>(`/auth/users/${encodeURIComponent(userId)}/roles`);
}

export function grantRole(userId: ID, role: string): Promise<void> {
  return api.post<void>(`/auth/users/${encodeURIComponent(userId)}/roles`, { role });
}

export function revokeRole(userId: ID, role: string): Promise<void> {
  return api.delete<void>(`/auth/users/${encodeURIComponent(userId)}/roles/${encodeURIComponent(role)}`);
}

export function listIdentities(userId: ID): Promise<{ items: ExternalIdentity[] }> {
  return api.get<{ items: ExternalIdentity[] }>(`/auth/users/${encodeURIComponent(userId)}/identities`);
}

export function linkIdentity(userId: ID, input: LinkIdentityInput): Promise<ExternalIdentity> {
  return api.post<ExternalIdentity>(`/auth/users/${encodeURIComponent(userId)}/identities`, input);
}

export function unlinkIdentity(userId: ID, identityId: ID): Promise<void> {
  return api.delete<void>(
    `/auth/users/${encodeURIComponent(userId)}/identities/${encodeURIComponent(identityId)}`,
  );
}

export function setIdentityDisabled(userId: ID, identityId: ID, disabled: boolean): Promise<void> {
  return api.post<void>(
    `/auth/users/${encodeURIComponent(userId)}/identities/${encodeURIComponent(identityId)}/${disabled ? "disable" : "enable"}`,
  );
}

/** Local session fetch so this feature never imports features/auth. */
export function currentSession(): Promise<UserSession> {
  return api.get<UserSession>("/auth/me");
}
