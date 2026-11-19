import type { RouteObject } from "react-router-dom";
import { RestorePage } from "./pages/RestorePage";

export const restoreRoute: RouteObject = {
  path: "restore",
  element: <RestorePage />,
};
