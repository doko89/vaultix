import type { RouteObject } from "react-router-dom";
import { NotificationsPage } from "./pages/NotificationsPage";

export const notificationsRoute: RouteObject = {
  path: "notifications",
  element: <NotificationsPage />,
};
