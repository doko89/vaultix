import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

// Vaultix web placeholder. Full scaffold (router, providers, features)
// is deferred to the frontend team per TECH_SPEC section 1.2 and 5.
const rootEl = document.getElementById("root");
if (rootEl === null) {
  throw new Error("missing #root element");
}
createRoot(rootEl).render(
  <StrictMode>
    <h1>Vaultix</h1>
  </StrictMode>,
);
