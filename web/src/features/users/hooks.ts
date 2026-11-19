import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import type { ID } from "@/shared/types";
import { errorText } from "@/shared/lib/api";
import { useToasts } from "@/shared/ui/toast";
import {
  changeOwnPassword,
  createUser,
  currentSession,
  getUser,
  grantRole,
  linkIdentity,
  listIdentities,
  listRoles,
  listUserRoles,
  listUsers,
  revokeRole,
  setIdentityDisabled,
  setUserDisabled,
  setUserPassword,
  unlinkIdentity,
  updateUser,
} from "./api";
import type { CreateUserInput, LinkIdentityInput, UpdateUserInput } from "./types";

export const USERS_KEY = ["users"] as const;

export function useCurrentSession() {
  return useQuery({ queryKey: ["auth", "me"], queryFn: currentSession, staleTime: 60_000, retry: false });
}

export function useUsers(
  opts: { q?: string | undefined; limit?: number | undefined; offset?: number | undefined } = {},
) {
  const { q, limit, offset } = opts;
  return useQuery({
    queryKey: [...USERS_KEY, "list", q ?? "", limit ?? 25, offset ?? 0],
    queryFn: () => listUsers({ q, limit, offset }),
  });
}

export function useUser(userId: ID | undefined) {
  return useQuery({
    queryKey: [...USERS_KEY, "detail", userId ?? ""],
    queryFn: () => {
      if (userId === undefined) throw new Error("user id is required");
      return getUser(userId);
    },
    enabled: userId !== undefined,
  });
}

export function useRoles() {
  return useQuery({ queryKey: [...USERS_KEY, "roles"], queryFn: listRoles, staleTime: 60_000 });
}

export function useUserRoles(userId: ID | undefined) {
  return useQuery({
    queryKey: [...USERS_KEY, "user-roles", userId ?? ""],
    queryFn: () => {
      if (userId === undefined) throw new Error("user id is required");
      return listUserRoles(userId);
    },
    enabled: userId !== undefined,
  });
}

export function useIdentities(userId: ID | undefined) {
  return useQuery({
    queryKey: [...USERS_KEY, "identities", userId ?? ""],
    queryFn: () => {
      if (userId === undefined) throw new Error("user id is required");
      return listIdentities(userId);
    },
    enabled: userId !== undefined,
  });
}

function invalidateUsers(queryClient: ReturnType<typeof useQueryClient>) {
  void queryClient.invalidateQueries({ queryKey: [...USERS_KEY] });
}

function useUserMutation<TVars>(fn: (vars: TVars) => Promise<unknown>, title: string, onDone?: () => void) {
  const queryClient = useQueryClient();
  const { push } = useToasts();
  return useMutation({
    mutationFn: fn,
    onSuccess: () => {
      push({ tone: "success", title });
      invalidateUsers(queryClient);
      onDone?.();
    },
    onError: (err) => push({ tone: "error", title: `Could not: ${title.toLowerCase()}`, message: errorText(err) }),
  });
}

export function useCreateUser(onDone: () => void) {
  return useUserMutation((input: CreateUserInput) => createUser(input), "User created", onDone);
}

export function useUpdateUser(onDone: () => void) {
  return useUserMutation(
    ({ userId, input }: { userId: ID; input: UpdateUserInput }) => updateUser(userId, input),
    "User updated",
    onDone,
  );
}

export function useSetUserDisabled() {
  return useUserMutation(
    ({ userId, disabled }: { userId: ID; disabled: boolean }) => setUserDisabled(userId, disabled),
    "User updated",
  );
}

export function useSetUserPassword(onDone: () => void) {
  return useUserMutation(
    ({ userId, password }: { userId: ID; password: string }) => setUserPassword(userId, password),
    "Password set",
    onDone,
  );
}

export function useGrantRole() {
  return useUserMutation(({ userId, role }: { userId: ID; role: string }) => grantRole(userId, role), "Role granted");
}

export function useRevokeRole() {
  return useUserMutation(({ userId, role }: { userId: ID; role: string }) => revokeRole(userId, role), "Role revoked");
}

export function useLinkIdentity(onDone: () => void) {
  return useUserMutation(
    ({ userId, input }: { userId: ID; input: LinkIdentityInput }) => linkIdentity(userId, input),
    "Identity linked",
    onDone,
  );
}

export function useUnlinkIdentity() {
  return useUserMutation(
    ({ userId, identityId }: { userId: ID; identityId: ID }) => unlinkIdentity(userId, identityId),
    "Identity unlinked",
  );
}

export function useSetIdentityDisabled() {
  return useUserMutation(
    ({ userId, identityId, disabled }: { userId: ID; identityId: ID; disabled: boolean }) =>
      setIdentityDisabled(userId, identityId, disabled),
    "Identity updated",
  );
}

export function useChangeOwnPassword() {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const { push } = useToasts();
  return useMutation({
    mutationFn: ({ oldPassword, newPassword }: { oldPassword: string; newPassword: string }) =>
      changeOwnPassword(oldPassword, newPassword),
    onSuccess: () => {
      push({ tone: "success", title: "Password changed — please log in again" });
      queryClient.clear();
      void navigate("/login", { replace: true });
    },
    onError: (err) => push({ tone: "error", title: "Could not change password", message: errorText(err) }),
  });
}
