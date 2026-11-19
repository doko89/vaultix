import type { RouteObject } from "react-router-dom";
import { DashboardPage } from "./pages/DashboardPage";

export const dashboardRoute: RouteObject = {
  index: true,
  element: <DashboardPage />,
};
