import type { RouteObject } from "react-router-dom";
import { CatalogPage } from "./pages/CatalogPage";

export const catalogRoute: RouteObject = {
  path: "catalog",
  element: <CatalogPage />,
};
