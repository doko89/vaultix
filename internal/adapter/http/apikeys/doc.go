// Package apikeys is the self-contained HTTP surface for user API key
// management (PRD §7.1): list, issue, and revoke under /api/api-keys.
//
// It imports no sibling http packages: routing arrives through the minimal
// Router interface (satisfied by *chi.Mux and the root Router — see
// Mount), the use case through the Service interface (satisfied by
// *usecase/apikey.Service), session enforcement through Deps.Session, and
// the session actor through Deps.Actor.
//
// INTEGRATION NOTES (platform track):
//   - Mount: call apikeys.Mount(r, deps) with the root router and
//     Deps{Service, Session: gate.RequireSession, Actor: <closure over
//     authhttp.SessionFromContext>}. The CRUD routes are session-only by
//     design: API keys manage infrastructure, never themselves (a leaked
//     key must not mint new keys).
//   - Actor closure: func(r *http.Request) (string, bool) {
//     info, ok := authhttp.SessionFromContext(r.Context()); if !ok {
//     return "", false }; return info.User.ID, true }.
//   - Bearer acceptance on all API routes comes from the auth Gate itself
//     (Gate.RequireAuth routes `vxk_...` bearers to the key verifier — see
//     the scope mapping documented on auth.Gate); no per-slice change is
//     needed. Wire the verifier with Deps.Keys on authhttp.Mount or
//     Gate.SetKeyVerifier.
package apikeys
