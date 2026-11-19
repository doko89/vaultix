import type { RouteObject } from "react-router-dom";
import { StoragePage } from "./pages/StoragePage";

export const storageRoute: RouteObject = {
  path: "storage",
  element: <StoragePage />,
};
