import { zodResolver } from "@hookform/resolvers/zod";
import { DatabaseBackup, Loader2 } from "lucide-react";
import { useForm } from "react-hook-form";
import { Navigate, useSearchParams } from "react-router-dom";
import { z } from "zod";
import { oidcStartUrl } from "@/features/auth/api";
import { useLogin, useMe, usePublicProviders } from "@/features/auth/hooks";
import type { PublicProvider } from "@/features/auth/types";
import { ApiError, errorText } from "@/shared/lib/api";
import { Alert } from "@/shared/ui/alert";
import { Button } from "@/shared/ui/button";
import { Card, CardContent } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";

// DESIGN section 10: one /login route serving three configurations
// (local + OIDC, local only, OIDC only). No sidebar/topbar here.

const loginSchema = z.object({
  username: z.string().min(1, "Username wajib diisi"),
  password: z.string().min(1, "Password wajib diisi"),
});

type LoginForm = z.infer<typeof loginSchema>;

// IdP error codes from the OIDC callback handler (auth/handlers.go), mapped
// to actionable messages. Never render tokens or claims here.
const CALLBACK_ERRORS: Record<string, string> = {
  access_denied: "Akses login ditolak. Silakan coba lagi.",
  expired_code: "Sesi login kedaluwarsa. Silakan coba lagi.",
  expired: "Sesi login kedaluwarsa. Silakan coba lagi.",
  invalid_state: "Sesi login tidak valid. Silakan coba lagi.",
  invalid_request: "Permintaan login tidak valid. Silakan coba lagi.",
  unauthorized_client: "Aplikasi tidak diizinkan oleh provider. Hubungi administrator.",
  unsupported_response_type: "Provider tidak mendukung metode login ini. Hubungi administrator.",
  invalid_scope: "Cakupan login tidak valid. Hubungi administrator.",
  server_error: "Provider mengalami gangguan. Silakan coba lagi.",
  temporarily_unavailable: "Provider sibuk. Silakan coba lagi.",
  error: "Login tidak selesai. Silakan coba lagi.",
  no_role: "Akun Anda tidak memiliki akses. Hubungi administrator.",
};

export interface LoginPageProps {
  providers?: PublicProvider[];
  localLoginEnabled?: boolean;
}

export function LoginPage({ providers: propProviders, localLoginEnabled = true }: LoginPageProps) {
  const [searchParams, setSearchParams] = useSearchParams();
  const errorCode = searchParams.get("error") ?? "";
  const next = searchParams.get("next") ?? "/";
  const errorMessage = CALLBACK_ERRORS[errorCode];

  const { data: session, isLoading: sessionLoading } = useMe();
  const publicProviders = usePublicProviders();
  const login = useLogin();

  const {
    register,
    handleSubmit,
    formState: { errors },
  } = useForm<LoginForm>({ resolver: zodResolver(loginSchema) });

  if (!sessionLoading && session !== null && session !== undefined) {
    return <Navigate to={next.startsWith("/") ? next : "/"} replace />;
  }

  const providers = propProviders ?? publicProviders.data?.items ?? [];
  const showSso = providers.length > 0;
  const showDivider = showSso && localLoginEnabled;
  const busy = login.isPending;

  function loginErrorMessage(): string | undefined {
    if (login.error === null) return undefined;
    if (login.error instanceof ApiError && login.error.status === 401) {
      return "Username atau password salah";
    }
    return errorText(login.error);
  }
  const formError = loginErrorMessage();

  function onSubmit(values: LoginForm): void {
    login.mutate({ identifier: values.username.trim(), password: values.password, next });
  }

  function onSsoClick(providerId: string): void {
    window.location.href = oidcStartUrl(providerId);
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-surface px-4 py-8">
      <div className="w-full max-w-[400px]">
        <div className="mb-6 flex items-center justify-center gap-2">
          <span className="flex size-9 items-center justify-center rounded-md bg-primary text-primary-foreground">
            <DatabaseBackup className="size-5" aria-hidden />
          </span>
          <span className="text-xl font-semibold">Vaultix</span>
        </div>

        <Card>
          <CardContent className="pt-6">
            <h1 className="text-center text-2xl font-semibold">Masuk ke Vaultix</h1>

            {errorMessage !== undefined && (
              <div className="mt-4">
                <Alert
                  tone="error"
                  message={errorMessage}
                  actionLabel="Coba lagi"
                  onAction={() => void setSearchParams(next === "/" ? {} : { next })}
                />
              </div>
            )}

            {showSso && (
              <div className="mt-6 flex flex-col gap-2">
                {providers.map((provider) => (
                  <Button
                    key={provider.id}
                    type="button"
                    variant="outline"
                    className="w-full"
                    onClick={() => onSsoClick(provider.id)}
                  >
                    Masuk dengan {provider.name}
                  </Button>
                ))}
              </div>
            )}

            {showDivider && (
              <div className="my-6 flex items-center gap-3 text-xs text-gray-500">
                <span className="h-px flex-1 bg-border" aria-hidden />
                atau
                <span className="h-px flex-1 bg-border" aria-hidden />
              </div>
            )}

            {localLoginEnabled ? (
              <form
                className="mt-6 flex flex-col gap-4"
                onSubmit={(event) => void handleSubmit(onSubmit)(event)}
              >
                <div>
                  <label htmlFor="username" className="mb-1 block text-sm font-medium">
                    Username
                  </label>
                  <Input
                    id="username"
                    autoComplete="username"
                    disabled={busy}
                    {...register("username")}
                  />
                  {errors.username && (
                    <p className="mt-1 text-xs text-destructive">{errors.username.message}</p>
                  )}
                </div>
                <div>
                  <label htmlFor="password" className="mb-1 block text-sm font-medium">
                    Password
                  </label>
                  <Input
                    id="password"
                    type="password"
                    autoComplete="current-password"
                    disabled={busy}
                    {...register("password")}
                  />
                  {errors.password && (
                    <p className="mt-1 text-xs text-destructive">{errors.password.message}</p>
                  )}
                </div>
                {formError !== undefined && (
                  <p role="alert" className="text-sm text-destructive">
                    {formError}
                  </p>
                )}
                <Button type="submit" className="w-full" disabled={busy}>
                  {busy && <Loader2 className="animate-spin" aria-hidden />}
                  {busy ? "Memeriksa…" : "Masuk"}
                </Button>
              </form>
            ) : (
              <p className="mt-6 text-center text-sm text-gray-500">
                Login lokal dinonaktifkan administrator
              </p>
            )}
          </CardContent>
        </Card>

        <p className="mt-4 text-center text-xs text-gray-500">Vaultix · Dokumentasi</p>
      </div>
    </div>
  );
}
