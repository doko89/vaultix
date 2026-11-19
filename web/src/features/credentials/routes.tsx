import type { RouteObject } from "react-router-dom";
import { CredentialsPage } from "./pages/CredentialsPage";

export const credentialsRoute: RouteObject = {
  path: "credentials",
  element: <CredentialsPage />,
};
