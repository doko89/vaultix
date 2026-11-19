import { zodResolver } from "@hookform/resolvers/zod";
import { DatabaseBackup, Loader2 } from "lucide-react";
import { useForm } from "react-hook-form";
import { useSearchParams } from "react-router-dom";
import { z } from "zod";
import type { OidcProvider } from "@/features/settings/types";
import { Alert } from "@/shared/ui/alert";
import { Button } from "@/shared/ui/button";
import { Card, CardContent } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";

// DESIGN section 10: one /login route serving three configurations
// (local + OIDC, local only, OIDC only). No real auth wiring yet —
// submit/SSO handlers are stubs until the auth feature lands.

const loginSchema = z.object({
  username: z.string().min(1, "Username wajib diisi"),
  password: z.string().min(1, "Password wajib diisi"),
});

type LoginForm = z.infer<typeof loginSchema>;

const CALLBACK_ERRORS: Record<string, string> = {
  access_denied: "Akses login ditolak. Silakan coba lagi.",
  expired: "Sesi login kedaluwarsa. Silakan coba lagi.",
  invalid_state: "Sesi login tidak valid. Silakan coba lagi.",
  no_role: "Akun Anda tidak memiliki akses. Hubungi administrator.",
};

export interface LoginPageProps {
  providers?: OidcProvider[];
  localLoginEnabled?: boolean;
}

export function LoginPage({ providers = [], localLoginEnabled = true }: LoginPageProps) {
  const [searchParams, setSearchParams] = useSearchParams();
  const errorCode = searchParams.get("error") ?? "";
  const errorMessage = CALLBACK_ERRORS[errorCode];

  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<LoginForm>({ resolver: zodResolver(loginSchema) });

  async function onSubmit(_values: LoginForm): Promise<void> {
    // TODO(auth): wire local login against the Manager session API.
    await Promise.resolve();
  }

  function onSsoClick(_providerId: string): void {
    // TODO(auth): redirect to the OIDC authorization endpoint.
  }

  const showSso = providers.length > 0;
  const showDivider = showSso && localLoginEnabled;

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
                  onAction={() => void setSearchParams({})}
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
                    disabled={!provider.available}
                    title={provider.available ? undefined : provider.unavailableReason}
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
                    disabled={isSubmitting}
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
                    disabled={isSubmitting}
                    {...register("password")}
                  />
                  {errors.password && (
                    <p className="mt-1 text-xs text-destructive">{errors.password.message}</p>
                  )}
                </div>
                <Button type="submit" className="w-full" disabled={isSubmitting}>
                  {isSubmitting && <Loader2 className="animate-spin" aria-hidden />}
                  {isSubmitting ? "Memeriksa…" : "Masuk"}
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
