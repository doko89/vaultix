package agents

import (
	"context"
	"fmt"
	"net/http"

	"github.com/vaultix/vaultix/internal/domain"
	agentuc "github.com/vaultix/vaultix/internal/usecase/agent"
	discoveryuc "github.com/vaultix/vaultix/internal/usecase/discovery"
)

// withAgentAuth validates the agent bearer credential against the domain
// AgentCredential port and stashes it in the request context. Missing or
// invalid credentials -> 401; revoked agents -> 403.
func (h *handler) withAgentAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cred, err := h.agents.AuthenticateAgent(r.Context(), bearerToken(r))
		if err != nil {
			h.writeError(w, r, err)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), credentialKey, cred)))
	}
}

// handleEnroll redeems an enrollment token and registers the agent.
// POST /api/agent/enroll
func (h *handler) handleEnroll(w http.ResponseWriter, r *http.Request) {
	var req enrollRequest
	if err := decodeJSON(r, &req); err != nil {
		h.writeError(w, r, err)
		return
	}
	if err := h.check(req); err != nil {
		h.writeError(w, r, err)
		return
	}
	ag, token, err := h.agents.RedeemEnrollmentToken(r.Context(), req.Token, agentuc.Registration{
		Hostname: req.Hostname, IPAddress: req.IPAddress, OS: req.OS,
		Architecture: req.Architecture, Version: req.Version, Capabilities: req.Capabilities,
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, enrollResponse{
		AgentID: ag.ID, Token: token, EnrolledAt: ag.EnrolledAt,
	})
}

// handleHeartbeat ingests one heartbeat; the agent identity comes from the
// credential, never from the body.
// POST /api/agent/heartbeat
func (h *handler) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	cred := credentialFrom(r.Context())
	if cred == nil {
		h.writeError(w, r, agentuc.ErrUnauthorized)
		return
	}
	var req heartbeatRequest
	if err := decodeJSON(r, &req); err != nil {
		h.writeError(w, r, err)
		return
	}
	if err := h.check(req); err != nil {
		h.writeError(w, r, err)
		return
	}
	res, err := h.agents.Heartbeat(r.Context(), agentuc.HeartbeatInput{
		AgentID: cred.AgentID, Version: req.Version, IPAddress: req.IPAddress,
		DiskTotalBytes: req.DiskTotalBytes, DiskFreeBytes: req.DiskFreeBytes,
		RunningJobs: req.RunningJobs, DockerAvailable: req.DockerAvailable,
		Capabilities: req.Capabilities,
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, heartbeatResponse{
		OK: true, Status: string(res.Status), Outdated: res.Outdated, ServerTime: h.now(),
	})
}

// handlePoll returns dispatchable jobs for the authenticated agent.
// GET /api/agent/poll?limit=
func (h *handler) handlePoll(w http.ResponseWriter, r *http.Request) {
	cred := credentialFrom(r.Context())
	if cred == nil {
		h.writeError(w, r, agentuc.ErrUnauthorized)
		return
	}
	limit, _, err := parseLimitOffset(r.URL.Query(), defaultPollLimit, maxPollLimit)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	jobs, err := h.jobs.Poll(r.Context(), cred.AgentID, limit)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if jobs == nil {
		jobs = []QueuedJob{}
	}
	writeJSON(w, http.StatusOK, pollResponse{Jobs: jobs, ServerTime: h.now()})
}

// handleReport accepts a job report and/or a discovery snapshot.
// POST /api/agent/report
func (h *handler) handleReport(w http.ResponseWriter, r *http.Request) {
	cred := credentialFrom(r.Context())
	if cred == nil {
		h.writeError(w, r, agentuc.ErrUnauthorized)
		return
	}
	var req reportRequest
	if err := decodeJSON(r, &req); err != nil {
		h.writeError(w, r, err)
		return
	}
	if err := h.check(req); err != nil {
		h.writeError(w, r, err)
		return
	}
	if req.Job == nil && len(req.Resources) == 0 {
		h.writeError(w, r, fmt.Errorf("%w: report needs a job or at least one resource", domain.ErrValidation))
		return
	}
	resp := reportResponse{OK: true}
	if req.Job != nil {
		if !validJobState(req.Job.State) {
			h.writeError(w, r, fmt.Errorf("%w: unknown job state %q", domain.ErrValidation, req.Job.State))
			return
		}
		rep := JobReport{
			JobID: req.Job.JobID, State: domain.JobState(req.Job.State),
			Error: req.Job.Error, BytesRead: req.Job.BytesRead, BytesUploaded: req.Job.BytesUploaded,
		}
		if err := h.jobs.Report(r.Context(), cred.AgentID, rep); err != nil {
			h.writeError(w, r, err)
			return
		}
		resp.JobAccepted = true
	}
	if len(req.Resources) > 0 {
		inputs := make([]discoveryuc.ResourceInput, 0, len(req.Resources))
		for i, res := range req.Resources {
			kind := domain.ResourceKind(res.Kind)
			if !discoveryuc.ValidKind(kind) {
				h.writeError(w, r, fmt.Errorf("%w: resources[%d]: unknown kind %q", domain.ErrValidation, i, res.Kind))
				return
			}
			inputs = append(inputs, discoveryuc.ResourceInput{
				Kind: kind, Name: res.Name, Detail: res.Detail,
			})
		}
		n, err := h.discovery.Ingest(r.Context(), cred.AgentID, inputs)
		if err != nil {
			h.writeError(w, r, err)
			return
		}
		resp.ResourcesIngested = n
	}
	writeJSON(w, http.StatusOK, resp)
}
