// Package oidc is a stdlib OIDC Relying-Party client implementing
// domain.OIDCClient (PRD §7.1.1): discovery with manual endpoint override,
// Authorization Code + PKCE, strict ID-token validation, and cached JWKS
// with rotation refresh.
//
// NOTE (auth track): TECH_SPEC §2 names go-oidc + oauth2, but this track's
// scope allows no new go.mod dependencies besides golang.org/x/crypto, so
// the RP is implemented on net/http + stdlib crypto. Behavior matches the
// PRD contract; the platform track may swap this adapter for go-oidc behind
// the same domain.OIDCClient port without touching usecases.
package oidc
