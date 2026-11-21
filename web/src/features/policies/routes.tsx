import type { RouteObject } from "react-router-dom";
import { PoliciesPage } from "./pages/PoliciesPage";
import { PolicyDetailPage } from "./pages/PolicyDetailPage";
import { SchedulesPage } from "./pages/SchedulesPage";

export const policiesRoute: RouteObject = {
  path: "policies",
  element: <PoliciesPage />,
};

export const policyDetailRoute: RouteObject = {
  path: "policies/:policyId",
  element: <PolicyDetailPage />,
};

export const schedulesRoute: RouteObject = {
  path: "schedules",
  element: <SchedulesPage />,
};
