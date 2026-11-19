package agents

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
	agentuc "github.com/vaultix/vaultix/internal/usecase/agent"
)

var errBoom = errors.New("boom")

type fakeGrants struct {
	uploads   []ObjectGrant
	key       string
	uploadID  string
	partURLs  []string
	err       error
	lastJob   string
	lastAgent string
	lastObjs  []UploadObjectRequest
	lastSize  uint64
	lastPart  int64
	lastParts []MultipartPart
}

func (f *fakeGrants) IssueUploadGrants(_ context.Context, agentID, jobID string, objects []UploadObjectRequest) ([]ObjectGrant, error) {
	f.lastAgent, f.lastJob, f.lastObjs = agentID, jobID, objects
	if f.err != nil {
		return nil, f.err
	}
	return f.uploads, nil
}

func (f *fakeGrants) InitMultipartUpload(_ context.Context, agentID, jobID, _ string, sizeBytes uint64, partSize int64) (string, string, []string, error) {
	f.lastAgent, f.lastJob, f.lastSize, f.lastPart = agentID, jobID, sizeBytes, partSize
	if f.err != nil {
		return "", "", nil, f.err
	}
	return f.key, f.uploadID, f.partURLs, nil
}

func (f *fakeGrants) CompleteMultipartUpload(_ context.Context, agentID, jobID, _, _ string, parts []MultipartPart) error {
	f.lastAgent, f.lastJob, f.lastParts = agentID, jobID, parts
	return f.err
}

func (f *fakeGrants) AbortMultipartUpload(_ context.Context, agentID, jobID, _, _ string) error {
	f.lastAgent, f.lastJob = agentID, jobID
	return f.err
}

type fakeRestores struct {
	assignment *RestoreAssignment
	grants     []ObjectGrant
	err        error
	lastAgent  string
	lastID     string
	reports    []RestoreReport
}

func (f *fakeRestores) PollRestore(_ context.Context, agentID string) (*RestoreAssignment, error) {
	f.lastAgent = agentID
	if f.err != nil {
		return nil, f.err
	}
	return f.assignment, nil
}

func (f *fakeRestores) IssueDownloadGrants(_ context.Context, agentID, restoreID string, _ []string) ([]ObjectGrant, error) {
	f.lastAgent, f.lastID = agentID, restoreID
	if f.err != nil {
		return nil, f.err
	}
	return f.grants, nil
}

func (f *fakeRestores) ReportRestore(_ context.Context, agentID, restoreID string, rep RestoreReport) error {
	f.lastAgent, f.lastID = agentID, restoreID
	if f.err != nil {
		return f.err
	}
	f.reports = append(f.reports, rep)
	return nil
}

func v1Router() (*stubRouter, *fakeJobs, *fakeGrants, *fakeRestores) {
	agents := newFakeAgents()
	agents.agents["ag-1"] = agentuc.AgentView{
		Agent:  domain.Agent{ID: "ag-1", Hostname: "vm-01"},
		Status: domain.AgentStatusOnline,
	}
	jobs, grants, restores := &fakeJobs{}, &fakeGrants{}, &fakeRestores{}
	r := &stubRouter{}
	Mount(r, Deps{
		Agents: agents, Discovery: &fakeDiscovery{}, Jobs: jobs,
		Grants: grants, Restores: restores,
		Now:    func() time.Time { return time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC) },
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return r, jobs, grants, restores
}

func TestPollShapeEncryptionAndCancelled(t *testing.T) {
	r, jobs, _, _ := v1Router()
	jobs.jobs = []QueuedJob{{
		ID: "j1", PolicyID: "p1", PolicyVersion: 3, Kind: "filesystem",
		Targets: []JobTarget{{Type: "host_filesystem", Selector: "/data", Options: map[string]string{}}},
		State:   "assigned", Attempt: 1,
		Encryption: JobEncryption{Enabled: true, KeyRef: "enc-1", KeyB64: "a2V5"},
	}}
	jobs.cancelled = []string{"j-old"}

	rec := do(t, r.find("GET", "/api/agent/poll"), "GET", "/api/agent/poll", "", "agent-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var out pollResponse
	decodeBody(t, rec, &out)
	if len(out.Jobs) != 1 {
		t.Fatalf("want 1 job, got %+v", out)
	}
	if len(out.Jobs[0].Targets) != 1 || out.Jobs[0].Targets[0].Type != "host_filesystem" ||
		out.Jobs[0].Targets[0].Selector != "/data" || out.Jobs[0].Targets[0].Options == nil {
		t.Fatalf("structured targets not passed through: %+v", out.Jobs[0].Targets)
	}
	enc := out.Jobs[0].Encryption
	if !enc.Enabled || enc.KeyRef != "enc-1" || enc.KeyB64 != "a2V5" {
		t.Fatalf("encryption envelope not passed through: %+v", enc)
	}
	if len(out.CancelledJobIDs) != 1 || out.CancelledJobIDs[0] != "j-old" {
		t.Fatalf("cancelled ids not passed through: %+v", out.CancelledJobIDs)
	}

	// Empty backlogs serialize as [] (never null) for stable clients.
	jobs.jobs, jobs.cancelled = nil, nil
	rec = do(t, r.find("GET", "/api/agent/poll"), "GET", "/api/agent/poll", "", "agent-token")
	var out2 pollResponse
	decodeBody(t, rec, &out2)
	if out2.Jobs == nil || out2.CancelledJobIDs == nil {
		t.Fatalf("want non-nil slices, got %+v", out2)
	}
}

func TestPollCancelledLookupFailureFailsLoud(t *testing.T) {
	r, jobs, _, _ := v1Router()
	jobs.cancelErr = errBoom
	rec := do(t, r.find("GET", "/api/agent/poll"), "GET", "/api/agent/poll", "", "agent-token")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestReportWithManifestDrivesCompletion(t *testing.T) {
	r, jobs, _, _ := v1Router()
	rep := r.find("POST", "/api/agent/report")

	rec := do(t, rep, "POST", "/api/agent/report", `{"job":{
		"job_id":"j1","state":"uploading",
		"manifest":{"objects":[
			{"key":"jobs/j1/chunk-0","size_bytes":100,"sha256":"`+strings.Repeat("a", 64)+`"},
			{"key":"jobs/j1/manifest.json","size_bytes":50,"sha256":"`+strings.Repeat("c", 64)+`"}
		],"bytes_read":100,"compression":"zstd","encryption":{"enabled":false}}
	}}`, "agent-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if len(jobs.completions) != 1 {
		t.Fatalf("want completion path, reports=%d completions=%d", len(jobs.reports), len(jobs.completions))
	}
	got := jobs.completions[0]
	if got.JobID != "j1" || got.Manifest == nil || len(got.Manifest.Objects) != 2 {
		t.Fatalf("manifest not mapped: %+v", got)
	}
	if got.Manifest.Compression != "zstd" || got.Manifest.Encrypted == nil || *got.Manifest.Encrypted {
		t.Fatalf("manifest meta not mapped: %+v", got.Manifest)
	}
	var out reportResponse
	decodeBody(t, rec, &out)
	if !out.JobAccepted || out.Verified == nil || !*out.Verified || out.SnapshotID != "snap-1" {
		t.Fatalf("bad completion ack: %+v", out)
	}

	// Failed verification still answers 200 with verified:false + detail.
	jobs.outcome = &CompletionOutcome{Verified: false, Failures: []string{"jobs/j1/chunk-0: missing"}}
	rec = do(t, rep, "POST", "/api/agent/report", `{"job":{
		"job_id":"j1","state":"uploading",
		"manifest":{"objects":[{"key":"k","size_bytes":1,"sha256":"`+strings.Repeat("b", 64)+`"}]}
	}}`, "agent-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("failed verify: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	decodeBody(t, rec, &out)
	if out.Verified == nil || *out.Verified || len(out.VerifyFailures) != 1 {
		t.Fatalf("bad failed-verify ack: %+v", out)
	}
}

func TestReportManifestCompressionObjectForm(t *testing.T) {
	r, jobs, _, _ := v1Router()
	rep := r.find("POST", "/api/agent/report")

	// The agent sends compression as {"algo","level"}; the server keeps the
	// algorithm name and still drives completion.
	rec := do(t, rep, "POST", "/api/agent/report", `{"job":{
		"job_id":"j1","state":"uploading",
		"manifest":{"objects":[
			{"key":"jobs/j1/chunk-0","size_bytes":100,"sha256":"`+strings.Repeat("a", 64)+`"},
			{"key":"jobs/j1/manifest.json","size_bytes":50,"sha256":"`+strings.Repeat("c", 64)+`"}
		],"compression":{"algo":"zstd","level":3},"encryption":{"enabled":false}}
	}}`, "agent-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if len(jobs.completions) != 1 {
		t.Fatalf("want completion path, reports=%d completions=%d", len(jobs.reports), len(jobs.completions))
	}
	if got := jobs.completions[0]; got.Manifest == nil || got.Manifest.Compression != "zstd" {
		t.Fatalf("object compression not mapped: %+v", got.Manifest)
	}

	// A non-string, non-object compression is still a 400.
	rec = do(t, rep, "POST", "/api/agent/report", `{"job":{
		"job_id":"j1","state":"uploading",
		"manifest":{"objects":[{"key":"k","size_bytes":1,"sha256":"`+strings.Repeat("b", 64)+`"}],"compression":5}
	}}`, "agent-token")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestReportLegacyPathUnchanged(t *testing.T) {
	r, jobs, _, _ := v1Router()
	rec := do(t, r.find("POST", "/api/agent/report"), "POST", "/api/agent/report",
		`{"job":{"job_id":"j1","state":"running","bytes_read":5}}`, "agent-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if len(jobs.reports) != 1 || len(jobs.completions) != 0 {
		t.Fatalf("legacy report must use Report only: %+v", jobs)
	}
	var out reportResponse
	decodeBody(t, rec, &out)
	if out.Verified != nil || out.SnapshotID != "" {
		t.Fatalf("legacy ack must not carry completion fields: %+v", out)
	}
}

func TestReportManifestValidation(t *testing.T) {
	r, jobs, _, _ := v1Router()
	rep := r.find("POST", "/api/agent/report")
	for name, body := range map[string]string{
		"empty objects": `{"job":{"job_id":"j1","state":"uploading","manifest":{"objects":[]}}}`,
		"missing key":   `{"job":{"job_id":"j1","state":"uploading","manifest":{"objects":[{"size_bytes":1,"sha256":"abc"}]}}}`,
		"missing sha":   `{"job":{"job_id":"j1","state":"uploading","manifest":{"objects":[{"key":"k","size_bytes":1}]}}}`,
	} {
		if rec := do(t, rep, "POST", "/api/agent/report", body, "agent-token"); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: want 400, got %d (%s)", name, rec.Code, rec.Body.String())
		}
	}
	if len(jobs.completions) != 0 {
		t.Fatal("rejected manifests must not reach completion")
	}
}

func TestUploadGrantsFlow(t *testing.T) {
	r, _, grants, _ := v1Router()
	h := r.find("POST", "/api/agent/jobs/:id/upload-grants")
	if h == nil {
		t.Fatal("upload-grants route missing")
	}
	// Bearer gate applies.
	if rec := do(t, h, "POST", "/api/agent/jobs/j1/upload-grants", `{"objects":[{"key":"a"}]}`, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: want 401, got %d", rec.Code)
	}
	grants.uploads = []ObjectGrant{{Key: "jobs/j1/a", URL: "https://s3/a", ExpiresAt: time.Date(2026, 11, 19, 12, 15, 0, 0, time.UTC)}}
	rec := do(t, h, "POST", "/api/agent/jobs/j1/upload-grants",
		`{"objects":[{"key":"a","size_bytes":10}]}`, "agent-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if grants.lastAgent != "ag-1" || grants.lastJob != "j1" || len(grants.lastObjs) != 1 {
		t.Fatalf("grant call not scoped: %+v", grants)
	}
	var out uploadGrantsResponse
	decodeBody(t, rec, &out)
	if len(out.Grants) != 1 || out.Grants[0].Key != "jobs/j1/a" || out.Grants[0].URL == "" {
		t.Fatalf("bad grants shape: %+v", out)
	}
	// Empty objects rejected at the boundary.
	if rec := do(t, h, "POST", "/api/agent/jobs/j1/upload-grants", `{"objects":[]}`, "agent-token"); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty: want 400, got %d", rec.Code)
	}
}

func TestGrantsNotConfiguredIsExplicit(t *testing.T) {
	r, _, _, _ := testRouter() // no Grants/Restores wired
	h := r.find("POST", "/api/agent/jobs/:id/upload-grants")
	rec := do(t, h, "POST", "/api/agent/jobs/j1/upload-grants", `{"objects":[{"key":"a"}]}`, "agent-token")
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("want 501, got %d (%s)", rec.Code, rec.Body.String())
	}
	rh := r.find("POST", "/api/agent/restores/poll")
	rec = do(t, rh, "POST", "/api/agent/restores/poll", `{"agent_id":"ag-1"}`, "agent-token")
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("restore poll unwired: want 501, got %d", rec.Code)
	}
}

func TestMultipartFlow(t *testing.T) {
	r, _, grants, _ := v1Router()
	grants.key, grants.uploadID = "jobs/j1/big", "up-1"
	grants.partURLs = []string{"https://s3/p1", "https://s3/p2"}

	// Init returns the FULL part-URL list (no separate /parts call).
	rec := do(t, r.find("POST", "/api/agent/jobs/:id/multipart/init"), "POST",
		"/api/agent/jobs/j1/multipart/init", `{"key":"big","size_bytes":20000000,"part_size":16777216}`, "agent-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("init: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var initOut multipartInitResponse
	decodeBody(t, rec, &initOut)
	if initOut.Key != "jobs/j1/big" || initOut.UploadID != "up-1" || len(initOut.PartURLs) != 2 {
		t.Fatalf("bad init shape: %+v", initOut)
	}
	if grants.lastSize != 20000000 || grants.lastPart != 16777216 {
		t.Fatalf("sizes not passed through: %+v", grants)
	}
	// part_size is range-checked at the boundary.
	rec = do(t, r.find("POST", "/api/agent/jobs/:id/multipart/init"), "POST",
		"/api/agent/jobs/j1/multipart/init", `{"key":"big","size_bytes":10,"part_size":1024}`, "agent-token")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("small part_size: want 400, got %d", rec.Code)
	}

	partSHA := strings.Repeat("a", 64)
	rec = do(t, r.find("POST", "/api/agent/jobs/:id/multipart/complete"), "POST",
		"/api/agent/jobs/j1/multipart/complete", `{"key":"jobs/j1/big","upload_id":"up-1","parts":[{"number":1,"etag":"e","sha256":"`+partSHA+`","size_bytes":100}]}`, "agent-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("complete: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if len(grants.lastParts) != 1 || grants.lastParts[0].PartNumber != 1 ||
		grants.lastParts[0].ETag != "e" || grants.lastParts[0].SHA256 != partSHA ||
		grants.lastParts[0].SizeBytes != 100 {
		t.Fatalf("part checksums not passed through: %+v", grants.lastParts)
	}
	rec = do(t, r.find("POST", "/api/agent/jobs/:id/multipart/abort"), "POST",
		"/api/agent/jobs/j1/multipart/abort", `{"key":"jobs/j1/big","upload_id":"up-1"}`, "agent-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("abort: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	// Bad part numbers are range-checked at the boundary.
	rec = do(t, r.find("POST", "/api/agent/jobs/:id/multipart/complete"), "POST",
		"/api/agent/jobs/j1/multipart/complete", `{"key":"k","upload_id":"u","parts":[{"number":0,"etag":"e"}]}`, "agent-token")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad part: want 400, got %d", rec.Code)
	}
	// Pre-checksum agents are rejected with an actionable upgrade message,
	// never completed silently.
	for name, body := range map[string]string{
		"missing sha": `{"key":"k","upload_id":"u","parts":[{"number":1,"etag":"e","size_bytes":10}]}`,
		"missing len": `{"key":"k","upload_id":"u","parts":[{"number":1,"etag":"e","sha256":"` + partSHA + `"}]}`,
		"bad sha":     `{"key":"k","upload_id":"u","parts":[{"number":1,"etag":"e","sha256":"zz","size_bytes":10}]}`,
	} {
		rec = do(t, r.find("POST", "/api/agent/jobs/:id/multipart/complete"), "POST",
			"/api/agent/jobs/j1/multipart/complete", body, "agent-token")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: want 400, got %d (%s)", name, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "sha256") {
			t.Fatalf("%s: 400 must name the missing checksum: %s", name, rec.Body.String())
		}
	}
	if rec := do(t, r.find("POST", "/api/agent/jobs/:id/multipart/complete"), "POST",
		"/api/agent/jobs/j1/multipart/complete", `{"key":"k","upload_id":"u","parts":[{"number":1,"etag":"e"}]}`, "agent-token"); !strings.Contains(rec.Body.String(), "upgrade the agent") {
		t.Fatalf("legacy agent 400 must carry the upgrade remedy: %s", rec.Body.String())
	}
}

func TestRestorePollGrantReportFlow(t *testing.T) {
	r, _, _, restores := v1Router()

	// Nothing due -> explicit null.
	rec := do(t, r.find("POST", "/api/agent/restores/poll"), "POST",
		"/api/agent/restores/poll", `{"agent_id":"ag-1"}`, "agent-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("poll: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var pollOut restorePollResponse
	decodeBody(t, rec, &pollOut)
	if pollOut.Restore != nil {
		t.Fatalf("want null restore, got %+v", pollOut)
	}

	// agent_id must match the credential.
	rec = do(t, r.find("POST", "/api/agent/restores/poll"), "POST",
		"/api/agent/restores/poll", `{"agent_id":"ag-2"}`, "agent-token")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("mismatch: want 403, got %d (%s)", rec.Code, rec.Body.String())
	}

	// Due restore passes through with its full shape.
	restores.assignment = &RestoreAssignment{
		ID: "r1", SnapshotID: "snap-9", Kind: "file",
		Destination:     RestoreDestination{Mode: "alternate", Path: "/tmp/restore"},
		OverwritePolicy: "warn", Paths: []string{"/srv/a"},
		Objects:    []RestoreObject{{Key: "jobs/j0/chunk-0", SizeBytes: 100, SHA256: strings.Repeat("a", 64)}},
		Encryption: RestoreEncryption{Enabled: true, KeyRef: "enc-1", KeyB64: "a2V5"},
	}
	rec = do(t, r.find("POST", "/api/agent/restores/poll"), "POST",
		"/api/agent/restores/poll", `{"agent_id":"ag-1"}`, "agent-token")
	decodeBody(t, rec, &pollOut)
	if pollOut.Restore == nil || pollOut.Restore.ID != "r1" || pollOut.Restore.Destination.Mode != "alternate" {
		t.Fatalf("bad assignment shape: %+v", pollOut)
	}
	if len(pollOut.Restore.Objects) != 1 || !pollOut.Restore.Encryption.Enabled {
		t.Fatalf("objects/encryption not passed through: %+v", pollOut.Restore)
	}

	// Download grants + report are restore-scoped.
	restores.grants = []ObjectGrant{{Key: "jobs/j0/chunk-0", URL: "https://s3/d"}}
	rec = do(t, r.find("POST", "/api/agent/restores/:id/download-grants"), "POST",
		"/api/agent/restores/r1/download-grants", `{"keys":["jobs/j0/chunk-0"]}`, "agent-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("grants: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if restores.lastAgent != "ag-1" || restores.lastID != "r1" {
		t.Fatalf("grants not scoped: %+v", restores)
	}
	rec = do(t, r.find("POST", "/api/agent/restores/:id/report"), "POST",
		"/api/agent/restores/r1/report", `{"state":"succeeded"}`, "agent-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("report: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if len(restores.reports) != 1 || restores.reports[0].State != domain.JobSucceeded {
		t.Fatalf("restore report not mapped: %+v", restores.reports)
	}
	if rec := do(t, r.find("POST", "/api/agent/restores/:id/report"), "POST",
		"/api/agent/restores/r1/report", `{"state":"teleporting"}`, "agent-token"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad state: want 400, got %d", rec.Code)
	}
}

func TestMountSplit(t *testing.T) {
	deps := Deps{Agents: newFakeAgents(), Discovery: &fakeDiscovery{}, Jobs: &fakeJobs{}}
	ui, proto := &stubRouter{}, &stubRouter{}
	MountUI(ui, deps)
	MountProtocol(proto, deps)
	for _, rt := range ui.routes {
		if strings.HasPrefix(rt.pattern, "/api/agent/") {
			t.Fatalf("UI mount leaked protocol route %s %s", rt.method, rt.pattern)
		}
	}
	if len(ui.routes) != 7 {
		t.Fatalf("want 7 UI routes, got %d", len(ui.routes))
	}
	for _, rt := range proto.routes {
		if strings.HasPrefix(rt.pattern, "/api/agents/") || rt.pattern == "/api/agents" {
			t.Fatalf("protocol mount leaked UI route %s %s", rt.method, rt.pattern)
		}
	}
	if len(proto.routes) != 11 {
		t.Fatalf("want 11 protocol routes, got %d", len(proto.routes))
	}
}

func TestTransportSecureContext(t *testing.T) {
	if TransportSecure(context.Background()) {
		t.Fatal("missing mark must read insecure")
	}
	ctx := ContextWithTransportSecure(context.Background(), true)
	if !TransportSecure(ctx) {
		t.Fatal("TLS mark not honored")
	}
}
