import type { RouteObject } from "react-router-dom";
import { AgentsPage } from "./pages/AgentsPage";
import { ResourcesPage } from "./pages/ResourcesPage";

export const agentsRoute: RouteObject = {
  path: "agents",
  element: <AgentsPage />,
};

export const resourcesRoute: RouteObject = {
  path: "agents/:agentId/resources",
  element: <ResourcesPage />,
};
