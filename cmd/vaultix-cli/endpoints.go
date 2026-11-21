package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// HTTP methods used by the endpoint table.
const (
	mGet    = http.MethodGet
	mPost   = http.MethodPost
	mDelete = http.MethodDelete
)

// endpoint is one Manager API operation: the method plus a path template
// where {name} segments are expanded with escaped arguments.
type endpoint struct {
	method string
	path   string
}

// endpoints is the table of every Manager route the CLI calls. It is the
// single place to rename a path or method if the server side drifts: no
// command hardcodes a URL. The user-API-keys entries (/api/api-keys CRUD
// + Bearer auth) are reconciled against the server's exact shapes.
var endpoints = map[string]endpoint{
	// Auth (existing: internal/adapter/http/auth).
	"auth.me": {mGet, "/api/auth/me"},

	// Agents (existing: internal/adapter/http/agents MountUI).
	"agents.list":     {mGet, "/api/agents"},
	"agents.show":     {mGet, "/api/agents/{id}"},
	"agents.revoke":   {mPost, "/api/agents/{id}/revoke"},
	"agents.delete":   {mDelete, "/api/agents/{id}"},
	"agents.describe": {mGet, "/api/agents/{id}/update"},
	"agents.update":   {mPost, "/api/agents/{id}/update"},

	// Agent updates (internal/adapter/http/agents update.go).
	"updates.check":  {mPost, "/api/agents/updates/check"},
	"updates.list":   {mGet, "/api/agents/updates/artifacts"},
	"updates.delete": {mDelete, "/api/agents/updates/artifacts/{id}"},

	// Policies (existing: internal/adapter/http/policies Mount).
	"policies.list": {mGet, "/api/policies"},
	"policies.show": {mGet, "/api/policies/{id}"},

	// Jobs (existing: internal/adapter/http/jobs Mount; chi Route needs
	// the trailing slash on the collection).
	"jobs.list":   {mGet, "/api/jobs/"},
	"jobs.create": {mPost, "/api/jobs/"},
	"jobs.show":   {mGet, "/api/jobs/{id}"},
	"jobs.retry":  {mPost, "/api/jobs/{id}/retry"},
	"jobs.cancel": {mPost, "/api/jobs/{id}/cancel"},

	// Catalog (existing: internal/adapter/http/restore Mount).
	"catalog.list": {mGet, "/api/catalog"},

	// Restore (existing: internal/adapter/http/restore Mount; status
	// takes the job id as ?id=).
	"restore.plan":    {mPost, "/api/restore/plan"},
	"restore.confirm": {mPost, "/api/restore/confirm"},
	"restore.execute": {mPost, "/api/restore/execute"},
	"restore.status":  {mGet, "/api/restore/status"},

	// User API keys (internal/adapter/http/apikeys Mount).
	"apikeys.list":   {mGet, "/api/api-keys"},
	"apikeys.create": {mPost, "/api/api-keys"},
	"apikeys.revoke": {mDelete, "/api/api-keys/{id}"},
}

// expandEndpoint resolves a table entry into a method and a concrete path,
// substituting {name} segments with path-escaped args. Unknown names and
// unexpanded templates are internal errors (a test pins the table).
func expandEndpoint(name string, args map[string]string) (method, path string, err error) {
	ep, ok := endpoints[name]
	if !ok {
		return "", "", fmt.Errorf("unknown endpoint %q", name)
	}
	path = ep.path
	for k, v := range args {
		path = strings.ReplaceAll(path, "{"+k+"}", url.PathEscape(v))
	}
	if strings.Contains(path, "{") {
		return "", "", fmt.Errorf("endpoint %q missing path arguments", name)
	}
	return ep.method, path, nil
}
