import { createBrowserRouter, RouterProvider } from "react-router-dom";
import { AppShell } from "./layout";
import { LoginPage } from "./pages/LoginPage";
import { NotFoundPage } from "./pages/NotFoundPage";
import { agentsRoute, resourcesRoute } from "@/features/agents/routes";
import { auditLogsRoute } from "@/features/audit-logs/routes";
import { catalogRoute } from "@/features/catalog/routes";
import { credentialsRoute } from "@/features/credentials/routes";
import { dashboardRoute } from "@/features/dashboard/routes";
import { jobsRoute } from "@/features/jobs/routes";
import { notificationsRoute } from "@/features/notifications/routes";
import { policiesRoute, schedulesRoute } from "@/features/policies/routes";
import { restoreRoute } from "@/features/restore/routes";
import { settingsRoute } from "@/features/settings/routes";
import { storageRoute } from "@/features/storage/routes";
import { usersRoute } from "@/features/users/routes";

// One route per PRD section 8 section; each feature exposes only its
// route object (TECH_SPEC section 1.2). /login stands outside the shell.
const router = createBrowserRouter([
  { path: "/login", element: <LoginPage /> },
  {
    path: "/",
    element: <AppShell />,
    children: [
      dashboardRoute,
      agentsRoute,
      resourcesRoute,
      policiesRoute,
      schedulesRoute,
      jobsRoute,
      catalogRoute,
      restoreRoute,
      storageRoute,
      credentialsRoute,
      usersRoute,
      notificationsRoute,
      auditLogsRoute,
      settingsRoute,
    ],
  },
  { path: "*", element: <NotFoundPage /> },
]);

export function AppRouter() {
  return <RouterProvider router={router} />;
}
