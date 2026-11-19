import type { RouteObject } from "react-router-dom";
import { JobsPage } from "./pages/JobsPage";

export const jobsRoute: RouteObject = {
  path: "jobs",
  element: <JobsPage />,
};
