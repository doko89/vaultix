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
import { NavLink, Outlet, useNavigate } from "react-router-dom";
import { cn } from "@/shared/lib/utils";
import { Button } from "@/shared/ui/button";
import { Input } from "@/shared/ui/input";

// DESIGN section 3: sidebar (one entry per PRD section 8) + topbar
// (global search, Manager connectivity, user menu + logout) + content.
const NAV_ITEMS = [
  { to: "/", label: "Dashboard", icon: LayoutDashboard, end: true },
  { to: "/agents", label: "Agents", icon: Server, end: false },
  { to: "/policies", label: "Policies", icon: ShieldCheck, end: false },
  { to: "/schedules", label: "Schedules", icon: CalendarClock, end: false },
  { to: "/jobs", label: "Jobs", icon: History, end: false },
  { to: "/catalog", label: "Catalog", icon: Database, end: false },
  { to: "/restore", label: "Restore", icon: DatabaseBackup, end: false },
  { to: "/storage", label: "Storage", icon: HardDrive, end: false },
  { to: "/credentials", label: "Credentials", icon: KeyRound, end: false },
  { to: "/users", label: "Users & Roles", icon: Users, end: false },
  { to: "/notifications", label: "Notifications", icon: Bell, end: false },
  { to: "/audit-logs", label: "Audit Logs", icon: ScrollText, end: false },
  { to: "/settings", label: "Settings", icon: Settings, end: false },
] as const;

function SidebarNav({ onNavigate }: { onNavigate?: () => void }) {
  return (
    <nav aria-label="Primary" className="flex flex-col gap-1 p-3">
      {NAV_ITEMS.map((item) => (
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
  const navigate = useNavigate();

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
        <SidebarNav />
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
            <SidebarNav onNavigate={() => setDrawerOpen(false)} />
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
            {/* TODO(auth): wire real Manager connectivity + session. */}
            <span className="hidden items-center gap-2 text-xs text-gray-500 md:flex">
              <span className="size-2 rounded-full bg-success" aria-hidden />
              Manager connected
            </span>
            <span className="flex size-8 items-center justify-center rounded-full bg-primary/10 text-xs font-semibold text-primary">
              A
            </span>
            <Button variant="ghost" size="sm" onClick={() => void navigate("/login")}>
              <LogOut className="size-4" aria-hidden />
              Logout
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
