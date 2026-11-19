import type { RouteObject } from "react-router-dom";
import { AuditLogsPage } from "./pages/AuditLogsPage";

export const auditLogsRoute: RouteObject = {
  path: "audit-logs",
  element: <AuditLogsPage />,
};
