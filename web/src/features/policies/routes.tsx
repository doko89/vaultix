import type { RouteObject } from "react-router-dom";
import { PoliciesPage } from "./pages/PoliciesPage";
import { SchedulesPage } from "./pages/SchedulesPage";

export const policiesRoute: RouteObject = {
  path: "policies",
  element: <PoliciesPage />,
};

export const schedulesRoute: RouteObject = {
  path: "schedules",
  element: <SchedulesPage />,
};
