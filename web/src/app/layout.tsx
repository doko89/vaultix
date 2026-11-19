import { useState } from "react";
import {
  Bell,
  CalendarClock,
  Database,
  DatabaseBackup,
  HardDrive,
  History,
  KeyRound,
  LayoutDashboard,
  LogOut,
  Menu,
  ScrollText,
  Search,
  Server,
  Settings,
  ShieldCheck,
  Users,
  X,
} from "lucide-react";
import { NavLink, Navigate, Outlet, useLocation } from "react-router-dom";
import { displayName, isAdmin } from "@/features/auth/types";
import { useLogout, useMe } from "@/features/auth/hooks";
import { cn } from "@/shared/lib/utils";
import { Button } from "@/shared/ui/button";
import { Input } from "@/shared/ui/input";

// DESIGN section 3: sidebar (one entry per PRD section 8) + topbar
// (global search, Manager connectivity, user menu + logout) + content.
// DESIGN section 7: nav hides sections the role may not use (server still
// enforces). Backend matrix (auth/middleware.go): only the user/role/OIDC
// admin routes are role-gated (administrator); every other slice needs an
// authenticated session only.
const NAV_ITEMS = [
  { to: "/", label: "Dashboard", icon: LayoutDashboard, end: true, adminOnly: false },
  { to: "/agents", label: "Agents", icon: Server, end: false, adminOnly: false },
  { to: "/policies", label: "Policies", icon: ShieldCheck, end: false, adminOnly: false },
  { to: "/schedules", label: "Schedules", icon: CalendarClock, end: false, adminOnly: false },
  { to: "/jobs", label: "Jobs", icon: History, end: false, adminOnly: false },
  { to: "/catalog", label: "Catalog", icon: Database, end: false, adminOnly: false },
  { to: "/restore", label: "Restore", icon: DatabaseBackup, end: false, adminOnly: false },
  { to: "/storage", label: "Storage", icon: HardDrive, end: false, adminOnly: false },
  { to: "/credentials", label: "Credentials", icon: KeyRound, end: false, adminOnly: false },
  { to: "/users", label: "Users & Roles", icon: Users, end: false, adminOnly: true },
  { to: "/notifications", label: "Notifications", icon: Bell, end: false, adminOnly: false },
  { to: "/audit-logs", label: "Audit Logs", icon: ScrollText, end: false, adminOnly: false },
  { to: "/settings", label: "Settings", icon: Settings, end: false, adminOnly: false },
] as const;

function SidebarNav({ onNavigate, admin }: { onNavigate?: () => void; admin: boolean }) {
  return (
    <nav aria-label="Primary" className="flex flex-col gap-1 p-3">
      {NAV_ITEMS.filter((item) => !item.adminOnly || admin).map((item) => (
        <NavLink
          key={item.to}
          to={item.to}
          end={item.end}
          onClick={onNavigate}
          className={({ isActive }) =>
            cn(
              "flex items-center gap-3 rounded-md px-3 py-2 text-sm font-medium",
              isActive ? "bg-primary/10 text-primary" : "text-gray-500 hover:bg-surface",
            )
          }
        >
          <item.icon className="size-4 shrink-0" aria-hidden />
          {item.label}
        </NavLink>
      ))}
    </nav>
  );
}

export function AppShell() {
  const [drawerOpen, setDrawerOpen] = useState(false);
  const location = useLocation();
  const { data: session, isLoading, isError } = useMe();
  const logout = useLogout();
  const admin = isAdmin(session ?? undefined);

  if (!isLoading && session === null) {
    const next = `${location.pathname}${location.search}`;
    return <Navigate to={`/login?next=${encodeURIComponent(next)}`} replace />;
  }

  const user = session?.user;
  const initial = user !== undefined ? displayName(user).slice(0, 1).toUpperCase() : "?";

  return (
    <div className="flex min-h-screen">
      {/* Desktop sidebar */}
      <aside className="hidden w-60 shrink-0 border-r border-border bg-background lg:block">
        <div className="flex h-14 items-center gap-2 border-b border-border px-4">
          <span className="flex size-8 items-center justify-center rounded-md bg-primary text-primary-foreground">
            <DatabaseBackup className="size-4" aria-hidden />
          </span>
          <span className="text-base font-semibold">Vaultix</span>
        </div>
        <SidebarNav admin={admin} />
      </aside>

      {/* Mobile drawer (sidebar becomes a drawer below 1024px, DESIGN section 8) */}
      {drawerOpen && (
        <div className="fixed inset-0 z-40 lg:hidden" role="dialog" aria-modal="true">
          <div
            className="absolute inset-0 bg-black/40"
            onClick={() => setDrawerOpen(false)}
            aria-hidden
          />
          <aside className="absolute inset-y-0 left-0 w-60 bg-background">
            <div className="flex h-14 items-center justify-between border-b border-border px-4">
              <span className="text-base font-semibold">Vaultix</span>
              <Button variant="ghost" size="icon" onClick={() => setDrawerOpen(false)}>
                <X className="size-4" aria-hidden />
                <span className="sr-only">Close navigation</span>
              </Button>
            </div>
            <SidebarNav admin={admin} onNavigate={() => setDrawerOpen(false)} />
          </aside>
        </div>
      )}

      <div className="flex min-w-0 flex-1 flex-col">
        {/* Topbar */}
        <header className="flex h-14 shrink-0 items-center gap-3 border-b border-border bg-background px-4">
          <Button
            variant="ghost"
            size="icon"
            className="lg:hidden"
            onClick={() => setDrawerOpen(true)}
          >
            <Menu className="size-4" aria-hidden />
            <span className="sr-only">Open navigation</span>
          </Button>
          <div className="relative hidden max-w-sm flex-1 sm:block">
            <Search
              className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-gray-500"
              aria-hidden
            />
            <Input
              type="search"
              placeholder="Search agents, policies, jobs…"
              aria-label="Global search"
              className="pl-9"
            />
          </div>
          <div className="ml-auto flex items-center gap-3">
            <span className="hidden items-center gap-2 text-xs text-gray-500 md:flex">
              <span
                className={cn("size-2 rounded-full", isError ? "bg-destructive" : "bg-success")}
                aria-hidden
              />
              {isError ? "Manager unreachable" : "Manager connected"}
            </span>
            <span
              className="flex size-8 items-center justify-center rounded-full bg-primary/10 text-xs font-semibold text-primary"
              title={user !== undefined ? displayName(user) : undefined}
            >
              {initial}
            </span>
            <Button
              variant="ghost"
              size="sm"
              onClick={() => logout.mutate()}
              disabled={logout.isPending}
            >
              <LogOut className="size-4" aria-hidden />
              {logout.isPending ? "…" : "Logout"}
            </Button>
          </div>
        </header>

        {/* Content */}
        <main className="min-w-0 flex-1 p-4 sm:p-6">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
