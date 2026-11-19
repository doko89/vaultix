import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { ApiError } from "@/shared/lib/api";
import { listPublicProviders, login, logout, me } from "./api";
import { isAdmin, roleNames } from "./types";
import type { Session } from "./types";

export const ME_QUERY_KEY = ["auth", "me"] as const;

/** Current session; 401 resolves to null (anonymous) instead of erroring. */
export function useMe() {
  return useQuery<Session | null>({
    queryKey: [...ME_QUERY_KEY],
    queryFn: async () => {
      try {
        return await me();
      } catch (err) {
        if (err instanceof ApiError && err.isAuth) return null;
        throw err;
      }
    },
    staleTime: 60_000,
    retry: false,
  });
}

export function useIsAdmin(): boolean {
  const { data } = useMe();
  return isAdmin(data ?? undefined);
}

export function useRoleNames(): string[] {
  const { data } = useMe();
  return roleNames(data ?? undefined);
}

export interface LoginVariables {
  identifier: string;
  password: string;
  next?: string;
}

export function useLogin() {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  return useMutation({
    mutationFn: ({ identifier, password }: LoginVariables) => login(identifier, password),
    onSuccess: (session, vars) => {
      queryClient.setQueryData([...ME_QUERY_KEY], session);
      const target = vars.next !== undefined && vars.next.startsWith("/") ? vars.next : "/";
      void navigate(target, { replace: true });
    },
  });
}

export function useLogout() {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  return useMutation({
    mutationFn: logout,
    onSettled: (result) => {
      queryClient.setQueryData([...ME_QUERY_KEY], null);
      queryClient.clear();
      const oidcUrl = result?.oidc_logout_url;
      if (oidcUrl !== undefined && oidcUrl !== "") {
        window.location.href = oidcUrl;
        return;
      }
      void navigate("/login", { replace: true });
    },
  });
}

export function usePublicProviders() {
  return useQuery({
    queryKey: ["auth", "providers", "public"],
    queryFn: listPublicProviders,
    staleTime: 5 * 60_000,
    retry: false,
  });
}
