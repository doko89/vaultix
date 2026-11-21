package agents

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-playground/validator/v10"
)

// Router is the chi-compatible subset this package needs. *chi.Mux satisfies
// it, so Mount accepts a chi router without this package importing chi
// (patterns below use chi ":id" syntax). Handlers parse path IDs from
// r.URL.Path directly, so they work under any router serving these shapes.
type Router interface {
	Get(pattern string, h http.HandlerFunc)
	Post(pattern string, h http.HandlerFunc)
	Put(pattern string, h http.HandlerFunc)
	Patch(pattern string, h http.HandlerFunc)
	Delete(pattern string, h http.HandlerFunc)
}

// Deps wires the package. Agents, Discovery, and Jobs are required; Now,
// Validate, and Logger default when nil. Grants and Restores are optional:
// grant/restore handlers answer an explicit 501 until they are wired.
type Deps struct {
	Agents    AgentService
	Discovery DiscoveryService
	Jobs      JobQueue
	Grants    GrantService
	Restores  RestoreAgentService

	Now      func() time.Time
	Validate *validator.Validate
	Logger   *slog.Logger
}

type handler struct {
	agents    AgentService
	discovery DiscoveryService
	jobs      JobQueue
	grants    GrantService
	restores  RestoreAgentService
	now       func() time.Time
	validate  *validator.Validate
	log       *slog.Logger
}

func newHandler(deps Deps) *handler {
	if deps.Agents == nil {
		panic("agents: Deps.Agents is required")
	}
	if deps.Discovery == nil {
		panic("agents: Deps.Discovery is required")
	}
	if deps.Jobs == nil {
		panic("agents: Deps.Jobs is required")
	}
	h := &handler{
		agents:    deps.Agents,
		discovery: deps.Discovery,
		jobs:      deps.Jobs,
		grants:    deps.Grants,
		restores:  deps.Restores,
		now:       deps.Now,
		validate:  deps.Validate,
		log:       deps.Logger,
	}
	if h.now == nil {
		h.now = func() time.Time { return time.Now().UTC() }
	}
	if h.validate == nil {
		h.validate = validator.New()
	}
	if h.log == nil {
		h.log = slog.Default()
	}
	return h
}

// Mount registers the UI endpoints (/api/agents...) and the agent protocol
// (/api/agent/...) on r. Prefer MountUI + MountProtocol when the caller
// gates the two surfaces differently (the http root does: session/RBAC for
// the UI, bearer auth inside the protocol handlers).
func Mount(r Router, deps Deps) {
	MountUI(r, deps)
	MountProtocol(r, deps)
}

// MountUI registers the Manager UI endpoints (/api/agents...). Session and
// RBAC enforcement is an upstream middleware owned by the http root, not
// this package.
func MountUI(r Router, deps Deps) {
	h := newHandler(deps)
	r.Post("/api/agents/enroll-tokens", h.handleIssueToken)
	r.Get("/api/agents", h.handleListAgents)
	r.Get("/api/agents/:id", h.handleGetAgent)
	r.Patch("/api/agents/:id", h.handlePatchAgent)
	r.Post("/api/agents/:id/revoke", h.handleRevokeAgent)
	r.Delete("/api/agents/:id", h.handleDeleteAgent)
	r.Get("/api/agents/:id/policies", h.handleAgentPolicies)
	r.Get("/api/agents/:id/resources", h.handleAgentResources)
}

// MountProtocol registers the agent protocol (/api/agent/...). Enroll
// redeems an enrollment token; the rest require agent bearer credentials
// validated against the AgentCredential port inside withAgentAuth.
func MountProtocol(r Router, deps Deps) {
	h := newHandler(deps)
	r.Post("/api/agent/enroll", h.handleEnroll)
	r.Post("/api/agent/heartbeat", h.withAgentAuth(h.handleHeartbeat))
	r.Get("/api/agent/poll", h.withAgentAuth(h.handlePoll))
	r.Post("/api/agent/report", h.withAgentAuth(h.handleReport))
	r.Post("/api/agent/jobs/:id/upload-grants", h.withAgentAuth(h.handleUploadGrants))
	r.Post("/api/agent/jobs/:id/multipart/init", h.withAgentAuth(h.handleMultipartInit))
	r.Post("/api/agent/jobs/:id/multipart/complete", h.withAgentAuth(h.handleMultipartComplete))
	r.Post("/api/agent/jobs/:id/multipart/abort", h.withAgentAuth(h.handleMultipartAbort))
	r.Post("/api/agent/restores/poll", h.withAgentAuth(h.handleRestorePoll))
	r.Post("/api/agent/restores/:id/download-grants", h.withAgentAuth(h.handleRestoreDownloadGrants))
	r.Post("/api/agent/restores/:id/report", h.withAgentAuth(h.handleRestoreReport))
}
