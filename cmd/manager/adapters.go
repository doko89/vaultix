// Package main wires the Phase 2 slices into the manager: repositories,
// usecases, adapters, and HTTP mounts. This file holds the small
// integration adapters that bridge signature gaps between tracks; the
// tracks' tested code is used as-is.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	agentshttp "github.com/vaultix/vaultix/internal/adapter/http/agents"
	platformhttp "github.com/vaultix/vaultix/internal/adapter/http/platform"
	"github.com/vaultix/vaultix/internal/adapter/repo"
	"github.com/vaultix/vaultix/internal/domain"
	apikeyuc "github.com/vaultix/vaultix/internal/usecase/apikey"
	audituc "github.com/vaultix/vaultix/internal/usecase/audit"
	authuc "github.com/vaultix/vaultix/internal/usecase/auth"
	backupuc "github.com/vaultix/vaultix/internal/usecase/backup"
	jobuc "github.com/vaultix/vaultix/internal/usecase/job"
	restoreuc "github.com/vaultix/vaultix/internal/usecase/restore"
	retentionuc "github.com/vaultix/vaultix/internal/usecase/retention"
	scheduleuc "github.com/vaultix/vaultix/internal/usecase/schedule"
	useruc "github.com/vaultix/vaultix/internal/usecase/user"

	"github.com/vaultix/vaultix/internal/adapter/crypto"
	"github.com/vaultix/vaultix/internal/adapter/scheduler"
	ustorage "github.com/vaultix/vaultix/internal/usecase/storage"
)

// Compile-time proofs that wiring targets satisfy the seams they fill.
var (
	_ ustorage.SecretBox             = (*storageSecretBox)(nil)
	_ authuc.ProviderSecretStore     = (*providerSecretStore)(nil)
	_ authuc.Auditor                 = (*auditSink)(nil)
	_ apikeyuc.Auditor               = (*auditSink)(nil)
	_ useruc.Auditor                 = (*auditSink)(nil)
	_ agentshttp.JobQueue            = (*agentJobQueue)(nil)
	_ scheduleuc.JobEnqueuer         = (*scheduleJobEnqueuer)(nil)
	_ scheduleuc.ConcurrencyGate     = (*jobConcurrencyGate)(nil)
	_ scheduleuc.RecurringRunner     = (*recurringRunner)(nil)
	_ scheduler.RetentionEnforcer    = (*retentionEnforcer)(nil)
	_ restoreuc.JobReporter          = (*restoreReporter)(nil)
	_ platformhttp.OIDCProviderAdmin = (*oidcProviderAdmin)(nil)
	_ platformhttp.DashboardSource   = (*dashboardSource)(nil)
	_ backupuc.ObjectAccess          = (*objectHead)(nil)
	_ retentionuc.ObjectAccess       = (*objectDelete)(nil)
)

// ---- storage secret box ----

// storageSecretBox adapts crypto.Box (string envelopes, no context) to the
// storage track's SecretBox (byte ciphertext, keyRef, context).
type storageSecretBox struct {
	box *crypto.Box
}

// storageKeyRef is the key slot recorded on every sealed secret. The
// envelope is self-describing ("v1:..."), so rotation only needs the
// slot name for bookkeeping.
const storageKeyRef = "v1"

func (s *storageSecretBox) Seal(_ context.Context, plaintext []byte) ([]byte, string, error) {
	env, err := s.box.Encrypt(plaintext)
	if err != nil {
		return nil, "", err
	}
	return []byte(env), storageKeyRef, nil
}

func (s *storageSecretBox) Open(_ context.Context, ciphertext []byte, _ string) ([]byte, error) {
	return s.box.Decrypt(string(ciphertext))
}

// ---- OIDC provider secret store ----

// providerSecretStore implements auth.ProviderSecretStore on
// oidc_providers.client_secret_encrypted with hand-written parameterized
// pgx (the repos track owns sqlc; only GetProviderSecret exists there, so
// Set/Delete ride here until the queries land).
type providerSecretStore struct {
	pool *pgxpool.Pool
}

func (s providerSecretStore) GetProviderSecret(ctx context.Context, providerID string) (string, error) {
	var secret *string
	err := s.pool.QueryRow(ctx,
		`SELECT client_secret_encrypted FROM oidc_providers WHERE id = $1::uuid`,
		providerID).Scan(&secret)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("provider %s: %w", providerID, domain.ErrNotFound)
		}
		return "", fmt.Errorf("get provider secret: %w", err)
	}
	if secret == nil {
		return "", nil
	}
	return *secret, nil
}

func (s providerSecretStore) SetProviderSecret(ctx context.Context, providerID, ciphertext string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE oidc_providers SET client_secret_encrypted = $2, updated_at = now() WHERE id = $1::uuid`,
		providerID, ciphertext)
	if err != nil {
		return fmt.Errorf("set provider secret: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("provider %s: %w", providerID, domain.ErrNotFound)
	}
	return nil
}

func (s providerSecretStore) DeleteProviderSecret(ctx context.Context, providerID string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE oidc_providers SET client_secret_encrypted = NULL, updated_at = now() WHERE id = $1::uuid`,
		providerID)
	if err != nil {
		return fmt.Errorf("delete provider secret: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("provider %s: %w", providerID, domain.ErrNotFound)
	}
	return nil
}

// ---- audit sink ----

// auditSink implements the auth, user, and http/auth auditors over the
// audit usecase. One instance is wired into all three.
type auditSink struct {
	audit *audituc.Service
}

func (s *auditSink) RecordAuthEvent(ctx context.Context, e authuc.AuditEvent) error {
	action, targetType := authAuditAction(e.Action, e.Result)
	return s.append(ctx, e.Actor, action, targetType, e.Target, e.Result, e.Detail, e.IP)
}

func (s *auditSink) RecordUserEvent(ctx context.Context, e useruc.AuditEvent) error {
	action := domain.AuditUserChange
	if strings.HasPrefix(e.Action, "user.role.") {
		action = domain.AuditRoleChange
	}
	return s.append(ctx, e.Actor, action, "user", e.Target, e.Result, e.Detail, "")
}

func (s *auditSink) append(ctx context.Context, actor string, action domain.AuditAction, targetType, target, result, detail, ip string) error {
	actorID := ""
	actorType := "system"
	if isUUID(actor) {
		actorID, actorType = actor, "user"
	} else if actor != "" && actor != "anonymous" {
		// The audit schema keys actor_id to users(id); non-UUID labels
		// (usernames, login identifiers) ride in the detail instead.
		detail = strings.TrimSpace("actor:" + actor + " " + detail)
	}
	_, err := s.audit.Append(ctx, audituc.Entry{
		ActorType:  actorType,
		ActorID:    actorID,
		Action:     action,
		TargetType: targetType,
		TargetID:   target,
		Outcome:    auditOutcome(result),
		Detail:     detail,
		IPAddress:  ip,
	})
	return err
}

// authAuditAction maps auth event names onto canonical audit actions.
func authAuditAction(action, result string) (domain.AuditAction, string) {
	ok := result == "success"
	switch {
	case action == "auth.login" || action == "auth.oidc.login":
		if ok {
			return domain.AuditLogin, "user"
		}
		return domain.AuditLoginFailed, "user"
	case action == "auth.logout":
		return domain.AuditLogout, "session"
	case action == "auth.oidc.start" || action == "auth.oidc.callback":
		if ok {
			return domain.AuditLogin, "oidc_provider"
		}
		return domain.AuditLoginFailed, "oidc_provider"
	case strings.HasPrefix(action, "auth.provider."):
		return domain.AuditOIDCChange, "oidc_provider"
	case strings.HasPrefix(action, "auth.identity."):
		return domain.AuditOIDCLinkChange, "identity"
	default:
		// auth.local_login and future security toggles.
		return domain.AuditUserChange, ""
	}
}

func auditOutcome(result string) string {
	switch result {
	case "success":
		return audituc.OutcomeOK
	case "denied":
		return audituc.OutcomeDenied
	default:
		return audituc.OutcomeError
	}
}

func isUUID(s string) bool {
	var u pgtype.UUID
	return u.Scan(s) == nil && u.Valid
}

// ---- agents job queue ----

// agentJobQueue adapts *job.Service (PollJobs/ReportResult) to the agents
// track's JobQueue seam (Poll/CancelledIDs/Report/CompleteWithManifest).
// backup drives manifest completion (see jobproto.go); keys mints TLS-only
// data-key envelopes from the pinned policy version. Both may be nil in
// partial wirings (completion/issuance then report explicit errors).
type agentJobQueue struct {
	jobs     *jobuc.Service
	agents   domain.AgentRepository
	policies domain.PolicyRepository
	backup   *backupuc.Service
	keys     *keyIssuer
	log      *slog.Logger
}

// Poll assigns due jobs and maps them onto the narrow agent shape. The
// agent's advertised capabilities come from its stored row; targets come
// from the job's pinned policy version.
func (q *agentJobQueue) Poll(ctx context.Context, agentID string, limit int) ([]agentshttp.QueuedJob, error) {
	var capabilities []string
	if ag, err := q.agents.GetAgent(ctx, agentID); err == nil {
		capabilities = ag.Capabilities
	} else if !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}
	jobs, err := q.jobs.PollJobs(ctx, agentID, capabilities)
	if err != nil {
		return nil, err
	}
	out := make([]agentshttp.QueuedJob, 0, len(jobs))
	for _, j := range jobs {
		qj, ok, err := q.mapJob(ctx, j)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		out = append(out, qj)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// mapJob projects one job. It reports ok=false (skipping the job with a
// warning) when the pinned version is missing or targetless, so one bad
// job never starves the agent's queue.
func (q *agentJobQueue) mapJob(ctx context.Context, j *domain.BackupJob) (agentshttp.QueuedJob, bool, error) {
	v, err := q.policies.GetVersion(ctx, j.PolicyID, j.PolicyVersion)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			q.log.Warn("agent poll: pinned policy version missing; skipping job",
				"job_id", j.ID, "policy_id", j.PolicyID, "version", j.PolicyVersion)
			return agentshttp.QueuedJob{}, false, nil
		}
		return agentshttp.QueuedJob{}, false, err
	}
	if len(v.Targets) == 0 {
		q.log.Warn("agent poll: pinned version has no targets; skipping job",
			"job_id", j.ID, "policy_id", j.PolicyID, "version", j.PolicyVersion)
		return agentshttp.QueuedJob{}, false, nil
	}
	targets := make([]agentshttp.JobTarget, 0, len(v.Targets))
	for _, t := range v.Targets {
		opts := t.Options
		if opts == nil {
			opts = map[string]string{}
		}
		if t.Type == domain.TargetPostgres || t.Type == domain.TargetMySQL {
			attached, err := q.dbCredentialOptions(ctx, j, t, opts)
			if err != nil {
				// Refuse loudly: fail THIS job with the actionable reason
				// and drop it from the response so the agent can never
				// drift into trust auth. Other jobs still dispatch.
				if ferr := q.refuseJob(ctx, j, err.Error()); ferr != nil {
					return agentshttp.QueuedJob{}, false, ferr
				}
				q.log.Warn("agent poll: refusing job: database credential not issued",
					"job_id", j.ID, "policy_id", j.PolicyID,
					"target_type", string(t.Type), "credential_ref", t.CredentialRef,
					"reason", err.Error())
				return agentshttp.QueuedJob{}, false, nil
			}
			opts = attached
		}
		targets = append(targets, agentshttp.JobTarget{
			Type: string(t.Type), Selector: t.Selector, Options: opts,
		})
	}
	return agentshttp.QueuedJob{
		ID:            j.ID,
		PolicyID:      j.PolicyID,
		PolicyVersion: j.PolicyVersion,
		Kind:          projectJobKind(v.Targets[0].Type),
		Targets:       targets,
		State:         string(j.State),
		Attempt:       j.Attempt,
		Encryption:    q.jobEncryption(ctx, j.AgentID, v),
	}, true, nil
}

// projectJobKind maps the first pinned target type onto the agent runtime's
// JobKind vocabulary (internal/agent/job.go). Types without an agent-side
// counterpart keep their raw name so the agent rejects them explicitly
// instead of executing the wrong procedure.
func projectJobKind(t domain.TargetType) string {
	switch t {
	case domain.TargetHostFilesystem:
		return "filesystem"
	case domain.TargetDockerVolume, domain.TargetDockerConfig:
		return "docker-volume"
	case domain.TargetPostgres:
		return "postgres"
	case domain.TargetMySQL:
		return "mysql"
	default:
		return string(t)
	}
}

// ---- DB credential issuance (PRD §7.7: secrets encrypted at rest,
// exposed only to the authorized agent for the minimum duration) ----

// dbCredentialAuditAction records DB credential issuance in the audit log.
// The detail names agent/job/ref/use only — never credential material.
const dbCredentialAuditAction = domain.AuditAction("db_credential_issue")

// dbCredentialRefusedCode stamps jobs refused at poll time because their
// database credential could not be issued.
const dbCredentialRefusedCode = "credential_refused"

// dbCredentialOptionKey is the target-options key carrying the issued
// credential object on the poll wire shape.
const dbCredentialOptionKey = "db_credential"

// dbCredential is the short-lived, job-scoped database credential attached
// to a poll target's options as the "db_credential" value (a JSON object).
// It is minted per poll over TLS only and must never be logged.
type dbCredential struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
}

// dbCredentialOptions resolves t's credential profile and returns a COPY of
// opts carrying the short-lived "db_credential" object (the pinned version
// is never mutated). The credential_ref itself is not echoed: the agent
// gets only the resolved credential for this job.
//
// Refusals are secret-free actionable errors (missing ref, insecure
// channel, unavailable/undecryptable/incomplete profile); callers fail the
// job with them instead of delivering it for trust auth.
func (q *agentJobQueue) dbCredentialOptions(ctx context.Context, j *domain.BackupJob, t domain.BackupPolicyTarget, opts map[string]string) (map[string]string, error) {
	ref := strings.TrimSpace(t.CredentialRef)
	if ref == "" {
		return nil, fmt.Errorf("jobproto: job %s: %s target %q carries no credential-profile reference; "+
			"refusing to run without issued credentials (set credential_ref on the target)",
			j.ID, t.Type, t.Selector)
	}
	// Same gate as key_b64 issuance: call TransportSecure rather than
	// reading TLS state here, so proxy-aware transports stay in one place.
	if !agentshttp.TransportSecure(ctx) {
		return nil, fmt.Errorf("jobproto: job %s: refusing to issue database credential %q "+
			"over an insecure channel (the agent must poll over TLS)", j.ID, ref)
	}
	cred, err := q.keys.openDBCredential(ctx, j.AgentID, j.ID, ref)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(cred)
	if err != nil {
		return nil, fmt.Errorf("jobproto: job %s: encode database credential: %v", j.ID, err)
	}
	out := make(map[string]string, len(opts)+1)
	for k, v := range opts {
		out[k] = v
	}
	out[dbCredentialOptionKey] = string(raw)
	return out, nil
}

// refuseJob fails one polled job with a secret-free actionable reason: stamp
// the message, walk the early state forward, then land the terminal report.
// Polled jobs are assigned, which cannot fail directly (assigned can only
// reach running/cancelled/queued), hence the walk.
func (q *agentJobQueue) refuseJob(ctx context.Context, j *domain.BackupJob, reason string) error {
	outcome := jobuc.Outcome{ErrorCode: dbCredentialRefusedCode, ErrorMessage: reason}
	if _, err := q.jobs.ReportResult(ctx, j.AgentID, j.ID, outcome); err != nil {
		return fmt.Errorf("jobproto: refuse job %s: %w", j.ID, err)
	}
	if err := q.walkJobTo(ctx, j.ID, domain.JobFailed); err != nil {
		return fmt.Errorf("jobproto: refuse job %s: %w", j.ID, err)
	}
	outcome.State = domain.JobFailed
	if _, err := q.jobs.ReportResult(ctx, j.AgentID, j.ID, outcome); err != nil {
		return fmt.Errorf("jobproto: refuse job %s: %w", j.ID, err)
	}
	return nil
}

// openDBCredential resolves a database secret profile into its wire shape.
// It fails closed with secret-free actionable errors: missing or
// undecryptable profiles, malformed payloads, missing required fields, and
// audit failures all withhold the credential. Like openDataKey (jobproto.go),
// issuance is audited before return — without a trail, no credential.
func (k *keyIssuer) openDBCredential(ctx context.Context, agentID, jobID, ref string) (dbCredential, error) {
	var zero dbCredential
	if k == nil || k.store == nil || k.box == nil {
		return zero, fmt.Errorf("jobproto: job %s: database credential resolution is not wired", jobID)
	}
	log := k.log
	if log == nil {
		log = slog.Default()
	}
	sec, err := k.store.GetSecret(ctx, ref)
	if err != nil {
		log.Warn("db credential issuance: secret profile unavailable",
			"agent_id", agentID, "job_id", jobID, "credential_ref", ref)
		return zero, fmt.Errorf("jobproto: job %s: database credential profile %q is unavailable; "+
			"check the profile still exists and the target's credential_ref is current", jobID, ref)
	}
	plain, err := k.box.Open(ctx, sec.Ciphertext, sec.KeyRef)
	if err != nil {
		log.Warn("db credential issuance: secret undecryptable",
			"agent_id", agentID, "job_id", jobID, "credential_ref", ref)
		return zero, fmt.Errorf("jobproto: job %s: database credential profile %q is undecryptable; "+
			"rotate the profile to re-seal it under the current key", jobID, ref)
	}
	cred, err := parseDBCredential(plain)
	if err != nil {
		log.Warn("db credential issuance: profile payload invalid",
			"agent_id", agentID, "job_id", jobID, "credential_ref", ref)
		return zero, fmt.Errorf("jobproto: job %s: database credential profile %q is invalid: %v", jobID, ref, err)
	}
	if k.audit != nil {
		if _, err := k.audit.Append(ctx, audituc.Entry{
			ActorType:  "agent",
			Action:     dbCredentialAuditAction,
			TargetType: "secret",
			TargetID:   ref,
			Outcome:    audituc.OutcomeOK,
			Detail:     "agent:" + agentID + " job:" + jobID + " use:poll",
		}); err != nil {
			log.Warn("db credential issuance: audit append failed; withholding credential",
				"agent_id", agentID, "job_id", jobID, "credential_ref", ref)
			return zero, fmt.Errorf("jobproto: job %s: database credential audit trail failed; refusing issuance", jobID)
		}
	}
	return cred, nil
}

// parseDBCredential decodes a database secret profile payload. The canonical
// shape mirrors the wire object: {"username","password","host?","port?"}.
// Passwords are verbatim (never trimmed); port accepts a JSON number or a
// numeric string. Errors name the offending field, never its value.
func parseDBCredential(plain []byte) (dbCredential, error) {
	var raw struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Host     string `json:"host"`
		Port     any    `json:"port"`
	}
	if err := json.Unmarshal(plain, &raw); err != nil {
		return dbCredential{}, fmt.Errorf("payload is not valid JSON")
	}
	cred := dbCredential{
		Username: strings.TrimSpace(raw.Username),
		Password: raw.Password,
		Host:     strings.TrimSpace(raw.Host),
	}
	if cred.Username == "" {
		return dbCredential{}, fmt.Errorf("missing required field %q", "username")
	}
	if cred.Password == "" {
		return dbCredential{}, fmt.Errorf("missing required field %q", "password")
	}
	switch p := raw.Port.(type) {
	case nil:
	case float64:
		if p != float64(int(p)) {
			return dbCredential{}, fmt.Errorf("field %q must be an integer", "port")
		}
		cred.Port = int(p)
	case string:
		n, err := parseDBPort(p)
		if err != nil {
			return dbCredential{}, err
		}
		cred.Port = n
	default:
		return dbCredential{}, fmt.Errorf("field %q must be a number", "port")
	}
	if cred.Port < 0 || cred.Port > 65535 {
		return dbCredential{}, fmt.Errorf("field %q is out of range 1..65535", "port")
	}
	return cred, nil
}

// parseDBPort decodes a string-encoded port; "" means unset. Strict: the
// whole string must be digits (no trailing garbage, no fractions).
func parseDBPort(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 || n > 65535 {
		return 0, fmt.Errorf("field %q is out of range 1..65535", "port")
	}
	return n, nil
}

// Report maps the agent's narrow report onto a job outcome. The single
// Error string becomes the message (no code); progress-only reports use
// the zero state, which ReportResult applies without transitioning.
func (q *agentJobQueue) Report(ctx context.Context, agentID string, rep agentshttp.JobReport) error {
	outcome := jobuc.Outcome{
		State:         rep.State,
		BytesRead:     rep.BytesRead,
		BytesUploaded: rep.BytesUploaded,
	}
	if rep.Error != "" {
		outcome.ErrorMessage = rep.Error
	}
	_, err := q.jobs.ReportResult(ctx, agentID, rep.JobID, outcome)
	if err == nil {
		return nil
	}
	if rep.State != domain.JobFailed || !errors.Is(err, domain.ErrConflict) {
		return err
	}
	// A failed report from an early state (its running progress was lost)
	// must still land: walk the job forward, then re-apply. Anything the
	// walk cannot fix surfaces the original conflict.
	if werr := q.walkJobTo(ctx, rep.JobID, domain.JobFailed); werr != nil {
		return err
	}
	_, err = q.jobs.ReportResult(ctx, agentID, rep.JobID, outcome)
	return err
}

// walkJobTo advances a job along the lifecycle chain until target is
// directly reachable. Terminal jobs are left for the caller to report on.
func (q *agentJobQueue) walkJobTo(ctx context.Context, jobID string, target domain.JobState) error {
	for i := 0; i <= len(restoreChain); i++ {
		j, err := q.jobs.Get(ctx, jobID)
		if err != nil {
			return err
		}
		if j.State == target || j.State.Terminal() {
			return nil
		}
		if j.State.CanTransitionTo(target) {
			return nil
		}
		idx := -1
		for n, s := range restoreChain {
			if s == j.State {
				idx = n
				break
			}
		}
		if idx < 0 || idx+1 >= len(restoreChain) {
			return fmt.Errorf("jobproto: cannot advance job %s toward %s", jobID, target)
		}
		if _, err := q.jobs.Transition(ctx, jobID, restoreChain[idx+1]); err != nil {
			return err
		}
	}
	return fmt.Errorf("jobproto: cannot advance job %s toward %s", jobID, target)
}

// ---- schedule seams over the jobs track ----

// scheduleJobEnqueuer implements schedule.JobEnqueuer over *job.Service.
// EnqueueScheduled pins the policy's current version and records the
// firing schedule on the job (the Fire snapshot available on BackupJob);
// the requested version is verified first so a concurrent publish fails
// loudly instead of pinning the wrong version.
type scheduleJobEnqueuer struct {
	jobs     *jobuc.Service
	policies domain.PolicyRepository
	repo     domain.BackupJobRepository
}

func (e *scheduleJobEnqueuer) EnqueueJob(ctx context.Context, req scheduleuc.EnqueueRequest) (*domain.BackupJob, error) {
	p, err := e.policies.GetPolicy(ctx, req.PolicyID)
	if err != nil {
		return nil, err
	}
	if req.PolicyVersion != 0 && p.CurrentVersion != req.PolicyVersion {
		return nil, fmt.Errorf("schedule: policy %s is at version %d, requested %d: %w",
			req.PolicyID, p.CurrentVersion, req.PolicyVersion, domain.ErrConflict)
	}
	var j *domain.BackupJob
	if req.ScheduleID == "" {
		j, err = e.jobs.EnqueueManual(ctx, req.AgentID, req.PolicyID, req.CreatedBy)
	} else {
		j, err = e.jobs.EnqueueScheduled(ctx, req.AgentID, req.PolicyID, req.ScheduleID)
	}
	if err != nil {
		return nil, err
	}
	if req.CreatedBy != "" && j.CreatedBy == "" {
		j.CreatedBy = req.CreatedBy
		if err := e.repo.UpdateJob(ctx, j); err != nil {
			return nil, err
		}
	}
	return j, nil
}

// jobConcurrencyGate implements schedule.ConcurrencyGate by counting
// non-terminal jobs, bucketed per agent and per policy.
type jobConcurrencyGate struct {
	repo domain.BackupJobRepository
}

func (g *jobConcurrencyGate) ActiveCounts(ctx context.Context) (scheduleuc.Counts, error) {
	counts := scheduleuc.Counts{PerAgent: map[string]int{}, PerPolicy: map[string]int{}}
	const pageSize = 500
	for offset := 0; ; offset += pageSize {
		res, err := g.repo.ListJobs(ctx,
			domain.ListOptions{Limit: pageSize, Offset: offset}, domain.JobFilter{})
		if err != nil {
			return scheduleuc.Counts{}, err
		}
		for _, j := range res.Items {
			if j.State.Terminal() {
				continue
			}
			counts.Global++
			counts.PerAgent[j.AgentID]++
			counts.PerPolicy[j.PolicyID]++
		}
		if len(res.Items) < pageSize {
			return counts, nil
		}
	}
}

// recurringRunner implements schedule.RecurringRunner over the in-process
// scheduler: one backup-execution job per agent. The first enqueue error
// aborts the firing so DispatchDue leaves NextRunAt untouched for retry.
type recurringRunner struct {
	sched *scheduler.Scheduler
}

func (r *recurringRunner) RunScheduled(ctx context.Context, fire scheduleuc.Fire) error {
	for _, agentID := range fire.AgentIDs {
		if err := r.sched.EnqueueBackup(ctx, scheduler.BackupExecutionArgs{
			AgentID:    agentID,
			PolicyID:   fire.PolicyID,
			ScheduleID: fire.Schedule.ID,
		}); err != nil {
			return fmt.Errorf("recurring run for schedule %s: %w", fire.Schedule.ID, err)
		}
	}
	return nil
}

// ---- retention enforcer ----

// retentionEnforcer implements scheduler.RetentionEnforcer over the
// retention usecase: load the policy rule, Evaluate, then Apply. Policies
// without a rule, and plans that would delete the last valid recovery
// point, are skipped without error (the recovery-point floor holds).
type retentionEnforcer struct {
	retention *retentionuc.Service
	rules     *repo.RetentionStore
	log       *slog.Logger
}

func (e *retentionEnforcer) EnforceRetention(ctx context.Context, policyID string) error {
	rec, err := e.rules.GetByPolicy(ctx, policyID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("retention rule for policy %s: %w", policyID, err)
	}
	plan, err := e.retention.Evaluate(ctx, policyID, rec.Rule,
		retentionuc.EvaluateOptions{Actor: "scheduler"})
	if err != nil {
		return err
	}
	if plan.OverrideRequired {
		e.log.Info("retention pass skipped: rule condemns the last valid recovery point",
			"policy_id", policyID, "plan_id", plan.ID)
		return nil
	}
	if _, err := e.retention.Apply(ctx, plan, retentionuc.ApplyOptions{}); err != nil {
		return err
	}
	if err := e.rules.RecordRun(ctx, policyID); err != nil {
		// The deletions already applied; a retry would be a harmless
		// no-op, so log instead of failing the pass.
		e.log.Warn("retention pass applied but run stamp failed", "policy_id", policyID, "error", err)
	}
	return nil
}

// ---- restore reporter ----

// restoreReporter implements restore.JobReporter over the restore-jobs
// repository. (The jobs service owns backup jobs only; restore jobs ride
// their own port.)
type restoreReporter struct {
	repo domain.RestoreJobRepository
}

func (r restoreReporter) CreateRestoreJob(ctx context.Context, job *domain.RestoreJob) error {
	return r.repo.CreateJob(ctx, job)
}

func (r restoreReporter) UpdateRestoreJob(ctx context.Context, job *domain.RestoreJob) error {
	return r.repo.UpdateJob(ctx, job)
}

func (r restoreReporter) GetRestoreJob(ctx context.Context, id string) (*domain.RestoreJob, error) {
	return r.repo.GetJob(ctx, id)
}

// ---- object access for backup/retention ----

// objectHead/objectDelete expose the registry's Head/Delete as the narrow
// backup/retention ObjectAccess seams.
type objectHead struct {
	objects domain.ObjectStorage
}

func (o objectHead) Head(ctx context.Context, destinationID, objectKey string) (domain.ObjectMeta, error) {
	return o.objects.Head(ctx, destinationID, objectKey)
}

type objectDelete struct {
	objects domain.ObjectStorage
}

func (o objectDelete) Delete(ctx context.Context, destinationID, objectKey string) error {
	return o.objects.Delete(ctx, destinationID, objectKey)
}

// ---- OIDC provider admin ----

// oidcProviderAdmin implements platform.OIDCProviderAdmin over the auth
// service. Deletes run raw SQL: the schema's identity/session references
// are ON DELETE SET NULL, so provider removal orphans nothing.
type oidcProviderAdmin struct {
	auth *authuc.Service
	pool *pgxpool.Pool
}

func (a *oidcProviderAdmin) CreateProvider(ctx context.Context, in platformhttp.OIDCProviderInput) (domain.OIDCProvider, error) {
	p, err := a.auth.CreateProvider(ctx, oidcInputToAuth(in), "")
	if err != nil {
		return domain.OIDCProvider{}, err
	}
	return *p, nil
}

func (a *oidcProviderAdmin) GetProvider(ctx context.Context, id string) (domain.OIDCProvider, error) {
	p, err := a.auth.GetProvider(ctx, id)
	if err != nil {
		return domain.OIDCProvider{}, err
	}
	return *p, nil
}

func (a *oidcProviderAdmin) ListProviders(ctx context.Context) ([]domain.OIDCProvider, error) {
	return a.auth.ListProviders(ctx)
}

func (a *oidcProviderAdmin) UpdateProvider(ctx context.Context, id string, in platformhttp.OIDCProviderInput) (domain.OIDCProvider, error) {
	p, err := a.auth.UpdateProvider(ctx, id, oidcInputToAuth(in), "")
	if err != nil {
		return domain.OIDCProvider{}, err
	}
	return *p, nil
}

func (a *oidcProviderAdmin) DeleteProvider(ctx context.Context, id string) error {
	tag, err := a.pool.Exec(ctx, `DELETE FROM oidc_providers WHERE id = $1::uuid`, id)
	if err != nil {
		return fmt.Errorf("delete provider: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("provider %s: %w", id, domain.ErrNotFound)
	}
	// TODO(auth): audit provider deletion (auth service has no delete path
	// to record through).
	return nil
}

func (a *oidcProviderAdmin) TestProvider(ctx context.Context, id string) error {
	rep, err := a.auth.TestProvider(ctx, id, "")
	if err != nil {
		return err
	}
	if !rep.OK {
		if rep.Error == "" {
			return errors.New("oidc: provider test failed")
		}
		return fmt.Errorf("oidc: provider test failed: %s", rep.Error)
	}
	return nil
}

func oidcInputToAuth(in platformhttp.OIDCProviderInput) authuc.ProviderInput {
	return authuc.ProviderInput{
		Name: in.Name, Issuer: in.Issuer,
		DiscoveryURL: in.DiscoveryURL,
		AuthURL:      in.AuthURL, TokenURL: in.TokenURL, JWKSURL: in.JWKSURL,
		UserInfoURL: in.UserInfoURL, EndSession: in.EndSession,
		ClientID: in.ClientID, ClientSecret: in.ClientSecret,
		Scopes:           append([]string(nil), in.Scopes...),
		GroupClaimNames:  append([]string(nil), in.GroupClaimNames...),
		GroupRoleMapping: copyStringMap(in.GroupRoleMapping),
		DefaultRole:      in.DefaultRole,
		JITProvisioning:  in.JITProvisioning,
		Enabled:          &in.Enabled,
	}
}

func copyStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// ---- dashboard source ----

// dashboardSource implements platform.DashboardSource by aggregating the
// repository ports. Listings page fully in memory; replace with SQL
// rollups when volume grows.
type dashboardSource struct {
	agents    domain.AgentRepository
	jobs      domain.BackupJobRepository
	policies  domain.PolicyRepository
	snapshots domain.SnapshotRepository
	clock     domain.Clock
	// heartbeatTimeout ages agents to stale/offline; zero selects the
	// domain default.
	heartbeatTimeout time.Duration
}

const dashboardPageSize = 500

func (d *dashboardSource) heartbeatTO() time.Duration {
	if d.heartbeatTimeout > 0 {
		return d.heartbeatTimeout
	}
	return domain.DefaultHeartbeatTimeout
}

func (d *dashboardSource) AgentCounts(ctx context.Context) (platformhttp.AgentCounts, error) {
	var out platformhttp.AgentCounts
	now := d.clock.Now()
	for offset := 0; ; offset += dashboardPageSize {
		res, err := d.agents.ListAgents(ctx, domain.ListOptions{Limit: dashboardPageSize, Offset: offset})
		if err != nil {
			return platformhttp.AgentCounts{}, err
		}
		for _, a := range res.Items {
			out.Total++
			switch a.Status(now, d.heartbeatTO()) {
			case domain.AgentStatusOnline:
				out.Online++
			case domain.AgentStatusOffline:
				out.Offline++
			case domain.AgentStatusStale:
				out.Stale++
			case domain.AgentStatusRevoked:
				out.Revoked++
			}
		}
		if len(res.Items) < dashboardPageSize {
			return out, nil
		}
	}
}

func (d *dashboardSource) JobsByStatus(ctx context.Context) (map[domain.JobState]int64, error) {
	out := map[domain.JobState]int64{}
	for offset := 0; ; offset += dashboardPageSize {
		res, err := d.jobs.ListJobs(ctx,
			domain.ListOptions{Limit: dashboardPageSize, Offset: offset}, domain.JobFilter{})
		if err != nil {
			return nil, err
		}
		for _, j := range res.Items {
			out[j.State]++
		}
		if len(res.Items) < dashboardPageSize {
			return out, nil
		}
	}
}

func (d *dashboardSource) RecentJobs(ctx context.Context, limit int) ([]platformhttp.JobSummary, error) {
	if limit <= 0 {
		limit = 25
	}
	jobs, err := d.allJobs(ctx)
	if err != nil {
		return nil, err
	}
	sort.Slice(jobs, func(i, k int) bool { return jobs[i].CreatedAt.After(jobs[k].CreatedAt) })
	if len(jobs) > limit {
		jobs = jobs[:limit]
	}
	out := make([]platformhttp.JobSummary, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, platformhttp.JobSummary{
			ID: j.ID, AgentID: j.AgentID, PolicyID: j.PolicyID, State: j.State,
			StartedAt:  formatTimePtr(j.StartedAt),
			FinishedAt: formatTimePtr(j.FinishedAt),
			Error:      firstNonEmpty(j.ErrorMessage, j.ErrorCode),
		})
	}
	return out, nil
}

func (d *dashboardSource) LastBackupPerAgent(ctx context.Context) ([]platformhttp.LastBackup, error) {
	jobs, err := d.allJobs(ctx)
	if err != nil {
		return nil, err
	}
	agents, err := d.allAgents(ctx)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, a := range agents {
		names[a.ID] = a.Hostname
	}
	latest := map[string]*domain.BackupJob{}
	succeeded := map[string]bool{}
	for i := range jobs {
		j := &jobs[i]
		if j.State == domain.JobSucceeded {
			succeeded[j.AgentID] = true
		}
		if cur, ok := latest[j.AgentID]; !ok || jobTime(j).After(jobTime(cur)) {
			latest[j.AgentID] = j
		}
	}
	out := make([]platformhttp.LastBackup, 0, len(agents))
	for _, a := range agents {
		lb := platformhttp.LastBackup{AgentID: a.ID, AgentName: a.Hostname, HasSucceeded: succeeded[a.ID]}
		if j, ok := latest[a.ID]; ok {
			lb.JobID, lb.State = j.ID, j.State
			lb.FinishedAt = formatTimePtr(j.FinishedAt)
			lb.PolicyID = j.PolicyID
		}
		out = append(out, lb)
	}
	return out, nil
}

func (d *dashboardSource) LastBackupPerPolicy(ctx context.Context) ([]platformhttp.LastBackup, error) {
	jobs, err := d.allJobs(ctx)
	if err != nil {
		return nil, err
	}
	policies, err := d.allPolicies(ctx)
	if err != nil {
		return nil, err
	}
	latest := map[string]*domain.BackupJob{}
	succeeded := map[string]bool{}
	for i := range jobs {
		j := &jobs[i]
		if j.State == domain.JobSucceeded {
			succeeded[j.PolicyID] = true
		}
		if cur, ok := latest[j.PolicyID]; !ok || jobTime(j).After(jobTime(cur)) {
			latest[j.PolicyID] = j
		}
	}
	out := make([]platformhttp.LastBackup, 0, len(policies))
	for _, p := range policies {
		lb := platformhttp.LastBackup{PolicyID: p.ID, PolicyName: p.Name, HasSucceeded: succeeded[p.ID]}
		if j, ok := latest[p.ID]; ok {
			lb.JobID, lb.State = j.ID, j.State
			lb.FinishedAt = formatTimePtr(j.FinishedAt)
			lb.AgentID = j.AgentID
		}
		out = append(out, lb)
	}
	return out, nil
}

func (d *dashboardSource) PoliciesWithoutRecentSuccess(ctx context.Context, olderThan time.Duration) ([]platformhttp.PolicyGap, error) {
	jobs, err := d.allJobs(ctx)
	if err != nil {
		return nil, err
	}
	policies, err := d.allPolicies(ctx)
	if err != nil {
		return nil, err
	}
	cutoff := d.clock.Now().Add(-olderThan)
	lastSuccess := map[string]time.Time{}
	for i := range jobs {
		j := &jobs[i]
		if j.State != domain.JobSucceeded {
			continue
		}
		if t := jobTime(j); t.After(lastSuccess[j.PolicyID]) {
			lastSuccess[j.PolicyID] = t
		}
	}
	var out []platformhttp.PolicyGap
	for _, p := range policies {
		last, ok := lastSuccess[p.ID]
		if ok && !last.Before(cutoff) {
			continue
		}
		gap := platformhttp.PolicyGap{PolicyID: p.ID, PolicyName: p.Name}
		if ok {
			s := last.UTC().Format(time.RFC3339)
			gap.LastSuccessAt = &s
		}
		out = append(out, gap)
	}
	return out, nil
}

func (d *dashboardSource) JobTrend(ctx context.Context, days int) ([]platformhttp.DayPoint, error) {
	if days <= 0 {
		days = 7
	}
	if days > 90 {
		days = 90
	}
	jobs, err := d.allJobs(ctx)
	if err != nil {
		return nil, err
	}
	today := d.clock.Now().UTC().Truncate(24 * time.Hour)
	points := make([]platformhttp.DayPoint, days)
	index := map[string]int{}
	for i := range points {
		day := today.AddDate(0, 0, i-days+1)
		points[i].Date = day.Format("2006-01-02")
		index[points[i].Date] = i
	}
	for i := range jobs {
		j := &jobs[i]
		day := j.CreatedAt.UTC().Format("2006-01-02")
		k, ok := index[day]
		if !ok {
			continue
		}
		switch j.State {
		case domain.JobSucceeded:
			points[k].Succeeded++
		case domain.JobFailed:
			points[k].Failed++
		}
		if j.BytesUploaded != nil {
			points[k].BytesUploaded += *j.BytesUploaded
		}
	}
	return points, nil
}

func (d *dashboardSource) StorageUsage(ctx context.Context) (platformhttp.StorageUsage, error) {
	var out platformhttp.StorageUsage
	for offset := 0; ; offset += dashboardPageSize {
		res, err := d.snapshots.ListSnapshots(ctx,
			domain.ListOptions{Limit: dashboardPageSize, Offset: offset}, domain.SnapshotFilter{})
		if err != nil {
			return platformhttp.StorageUsage{}, err
		}
		for _, s := range res.Items {
			out.SnapshotCount++
			out.ObjectCount += int64(s.ObjectCount)
			if s.BytesStored != nil {
				out.BytesUsed += *s.BytesStored
			}
		}
		if len(res.Items) < dashboardPageSize {
			out.Available = true
			return out, nil
		}
	}
}

func (d *dashboardSource) allJobs(ctx context.Context) ([]domain.BackupJob, error) {
	var all []domain.BackupJob
	for offset := 0; ; offset += dashboardPageSize {
		res, err := d.jobs.ListJobs(ctx,
			domain.ListOptions{Limit: dashboardPageSize, Offset: offset}, domain.JobFilter{})
		if err != nil {
			return nil, err
		}
		all = append(all, res.Items...)
		if len(res.Items) < dashboardPageSize {
			return all, nil
		}
	}
}

func (d *dashboardSource) allAgents(ctx context.Context) ([]domain.Agent, error) {
	var all []domain.Agent
	for offset := 0; ; offset += dashboardPageSize {
		res, err := d.agents.ListAgents(ctx, domain.ListOptions{Limit: dashboardPageSize, Offset: offset})
		if err != nil {
			return nil, err
		}
		all = append(all, res.Items...)
		if len(res.Items) < dashboardPageSize {
			return all, nil
		}
	}
}

func (d *dashboardSource) allPolicies(ctx context.Context) ([]domain.BackupPolicy, error) {
	var all []domain.BackupPolicy
	for offset := 0; ; offset += dashboardPageSize {
		res, err := d.policies.ListPolicies(ctx, domain.ListOptions{Limit: dashboardPageSize, Offset: offset})
		if err != nil {
			return nil, err
		}
		all = append(all, res.Items...)
		if len(res.Items) < dashboardPageSize {
			return all, nil
		}
	}
}

// jobTime prefers completion, falling back to creation.
func jobTime(j *domain.BackupJob) time.Time {
	if j.FinishedAt != nil {
		return *j.FinishedAt
	}
	return j.CreatedAt
}

func formatTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
