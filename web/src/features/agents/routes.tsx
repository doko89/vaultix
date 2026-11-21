import type { RouteObject } from "react-router-dom";
import { AgentDetailPage } from "./pages/AgentDetailPage";
import { AgentsPage } from "./pages/AgentsPage";
import { ResourcesPage } from "./pages/ResourcesPage";

export const agentsRoute: RouteObject = {
  path: "agents",
  element: <AgentsPage />,
};

export const agentDetailRoute: RouteObject = {
  path: "agents/:agentId",
  element: <AgentDetailPage />,
};

export const resourcesRoute: RouteObject = {
  path: "agents/:agentId/resources",
  element: <ResourcesPage />,
};
