package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// recordedRequest is one HTTP call the fake manager observed.
type recordedRequest struct {
	method string
	path   string
	query  url.Values
	body   string
	auth   string
}

// fakeManager is a scripted httptest Manager: routes map "METHOD path" to a
// canned status+body, and every request is recorded (with its Authorization
// header) for the request-table assertions.
type fakeManager struct {
	t        *testing.T
	server   *httptest.Server
	routes   map[string]fakeRoute
	requests []recordedRequest
}

type fakeRoute struct {
	status int
	body   string
}

func newFakeManager(t *testing.T) *fakeManager {
	t.Helper()
	f := &fakeManager{t: t, routes: map[string]fakeRoute{}}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeManager) url() string { return f.server.URL }

func (f *fakeManager) on(method, path string, body string) {
	f.routes[method+" "+path] = fakeRoute{status: http.StatusOK, body: body}
}

func (f *fakeManager) onStatus(method, path string, status int, body string) {
	f.routes[method+" "+path] = fakeRoute{status: status, body: body}
}

func (f *fakeManager) serve(w http.ResponseWriter, r *http.Request) {
	rawBody, _ := io.ReadAll(r.Body)
	f.requests = append(f.requests, recordedRequest{
		method: r.Method,
		path:   r.URL.Path,
		query:  r.URL.Query(),
		body:   string(rawBody),
		auth:   r.Header.Get("Authorization"),
	})
	route, ok := f.routes[r.Method+" "+r.URL.Path]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":404,"message":"fake: no route"}}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(route.status)
	_, _ = w.Write([]byte(route.body))
}

// execCLI runs the CLI with env credentials pointed at the fake manager and
// a fresh temp config file, returning exit code + captured streams.
func execCLI(t *testing.T, f *fakeManager, stdin string, args ...string) (int, string, string) {
	t.Helper()
	t.Setenv(envManagerURL, f.url())
	t.Setenv(envAPIKey, "vxk_test_key")
	cfg := filepath.Join(t.TempDir(), "cli.yaml")
	fullArgs := append([]string{"--config", cfg}, args...)
	var out, errB bytes.Buffer
	code := run(fullArgs, strings.NewReader(stdin), &out, &errB)
	return code, out.String(), errB.String()
}

// wantRequest is one expected row of the recorded request table.
type wantRequest struct {
	method string
	path   string
	query  map[string]string
	body   string // expected JSON; "" means no body expected
}

func assertRequestTable(t *testing.T, f *fakeManager, want []wantRequest) {
	t.Helper()
	if len(f.requests) != len(want) {
		t.Fatalf("recorded %d requests, want %d: %+v", len(f.requests), len(want), f.requests)
	}
	for i, w := range want {
		got := f.requests[i]
		if got.method != w.method || got.path != w.path {
			t.Fatalf("request %d = %s %s, want %s %s", i, got.method, got.path, w.method, w.path)
		}
		if got.auth != "Bearer vxk_test_key" {
			t.Fatalf("request %d auth = %q, want Bearer", i, got.auth)
		}
		for k, v := range w.query {
			if got.query.Get(k) != v {
				t.Fatalf("request %d query %q = %q, want %q (full: %v)", i, k, got.query.Get(k), v, got.query)
			}
		}
		if w.body == "" {
			if strings.TrimSpace(got.body) != "" {
				t.Fatalf("request %d body = %q, want empty", i, got.body)
			}
			continue
		}
		var gotJSON, wantJSON any
		if err := json.Unmarshal([]byte(got.body), &gotJSON); err != nil {
			t.Fatalf("request %d body is not JSON: %q", i, got.body)
		}
		if err := json.Unmarshal([]byte(w.body), &wantJSON); err != nil {
			t.Fatalf("bad test expectation: %v", err)
		}
		if !reflect.DeepEqual(gotJSON, wantJSON) {
			t.Fatalf("request %d body = %s, want %s", i, got.body, w.body)
		}
	}
}

const (
	fixtureMe      = `{"user":{"id":"u1","username":"alice","email":"alice@example.com","display_name":"Alice"},"roles":[{"id":"r1","name":"admin"},{"id":"r2","name":"operator"}],"expires_at":"2026-01-01T00:00:00Z"}`
	fixtureAgents  = `{"items":[{"id":"ag-1","hostname":"vm-1","status":"active","version":"1.2.0","last_heartbeat_at":"2026-11-01T10:00:00Z"},{"id":"ag-2","hostname":"vm-2","status":"offline","version":"1.1.0"}],"total":2}`
	fixtureAgent   = `{"id":"ag-1","hostname":"vm-1","status":"active","version":"1.2.0","os":"linux","architecture":"amd64","capabilities":["docker"],"disabled":false,"outdated":false,"enrolled_at":"2026-10-01T00:00:00Z","last_heartbeat_at":"2026-11-01T10:00:00Z"}`
	fixturePolices = `{"items":[{"id":"p-1","name":"daily","enabled":true,"current_version":3,"agent_ids":["ag-1"],"storage_id":"s3-main"}],"total":1}`
	fixturePolicy  = `{"id":"p-1","name":"daily","description":"daily backups","enabled":true,"current_version":3,"agent_ids":["ag-1"],"storage_id":"s3-main","compression":{"algorithm":"zstd","level":3},"encryption":{"enabled":true,"key_ref":"k1"},"created_by":"u1","created_at":"2026-09-01T00:00:00Z","updated_at":"2026-10-01T00:00:00Z"}`
	fixtureJobs    = `{"items":[{"id":"j-1","agent_id":"ag-1","policy_id":"p-1","policy_version":3,"trigger":"manual","state":"running","progress":12.5,"attempt":1,"created_at":"2026-11-01T09:00:00Z","updated_at":"2026-11-01T09:05:00Z"}],"total":1,"limit":50,"offset":0}`
	fixtureJob     = `{"id":"j-1","agent_id":"ag-1","policy_id":"p-1","policy_version":3,"trigger":"manual","state":"running","progress":12.5,"attempt":1,"created_at":"2026-11-01T09:00:00Z","updated_at":"2026-11-01T09:05:00Z","retry_history":[{"id":"j-0","attempt":0,"state":"failed"}]}`
	fixtureCatalog = `{"items":[{"id":"snap-1","agent_id":"ag-1","policy_id":"p-1","target_type":"docker_volume","selector":"vol1","status":"complete","restorable":true,"completed_at":"2026-11-01T08:00:00Z","created_at":"2026-11-01T07:00:00Z"}],"total":1,"limit":50,"offset":0}`
	fixturePlan    = `{"id":"plan-1","kind":"file","agent_id":"ag-1","snapshot_ids":["snap-1"],"steps":[{"sequence":1,"kind":"file","snapshot_id":"snap-1","source":"/data","destination":"/restore"}],"warnings":["target exists"],"requested_by":"alice","created_at":1762000000,"expires_at":1762003600}`
	fixtureConfirm = `{"token":"confirm-tok-1","plan_id":"plan-1","expires_at":"2026-11-01T12:00:00Z"}`
	fixtureRestore = `{"id":"rj-1","agent_id":"ag-1","snapshot_id":"snap-1","state":"queued","created_at":"2026-11-01T11:00:00Z","updated_at":"2026-11-01T11:00:00Z"}`
	fixtureKeys    = `{"items":[{"id":"kid-1","name":"ci","scopes":["read"],"key_prefix":"vxk_abc","expires_at":"2027-01-01T00:00:00Z","created_at":"2026-11-01T00:00:00Z"}],"total":1}`
	fixtureKeyNew  = `{"id":"kid-2","name":"ci-2","key":"vxk_shown_once_abc123"}`
)

// TestCommandRequestTable pins every command's method/path/body against the
// fake manager, mirroring the server's exact shapes.
func TestCommandRequestTable(t *testing.T) {
	policyTwoAgents := `{"id":"p-9","name":"multi","agent_ids":["ag-1","ag-2"]}`
	cases := []struct {
		name  string
		args  []string
		stdin string
		setup func(f *fakeManager)
		want  []wantRequest
	}{
		{"whoami", []string{"whoami"}, "", func(f *fakeManager) {
			f.on("GET", "/api/auth/me", fixtureMe)
		}, []wantRequest{{"GET", "/api/auth/me", nil, ""}}},

		{"agents list", []string{"agents", "list"}, "", func(f *fakeManager) {
			f.on("GET", "/api/agents", fixtureAgents)
		}, []wantRequest{{"GET", "/api/agents", nil, ""}}},
		{"agents show", []string{"agents", "show", "ag-1"}, "", func(f *fakeManager) {
			f.on("GET", "/api/agents/ag-1", fixtureAgent)
		}, []wantRequest{{"GET", "/api/agents/ag-1", nil, ""}}},

		{"policies list", []string{"policies", "list"}, "", func(f *fakeManager) {
			f.on("GET", "/api/policies", fixturePolices)
		}, []wantRequest{{"GET", "/api/policies", nil, ""}}},
		{"policies show", []string{"policies", "show", "p-1"}, "", func(f *fakeManager) {
			f.on("GET", "/api/policies/p-1", fixturePolicy)
		}, []wantRequest{{"GET", "/api/policies/p-1", nil, ""}}},

		{"jobs list with filters", []string{"jobs", "list", "--agent", "a1", "--state", "running", "--limit", "10"}, "", func(f *fakeManager) {
			f.on("GET", "/api/jobs/", fixtureJobs)
		}, []wantRequest{{"GET", "/api/jobs/", map[string]string{"agent_id": "a1", "state": "running", "limit": "10"}, ""}}},
		{"jobs show", []string{"jobs", "show", "j-1"}, "", func(f *fakeManager) {
			f.on("GET", "/api/jobs/j-1", fixtureJob)
		}, []wantRequest{{"GET", "/api/jobs/j-1", nil, ""}}},
		{"jobs retry", []string{"jobs", "retry", "j-1"}, "", func(f *fakeManager) {
			f.on("POST", "/api/jobs/j-1/retry", fixtureJob)
		}, []wantRequest{{"POST", "/api/jobs/j-1/retry", nil, ""}}},
		{"jobs cancel", []string{"jobs", "cancel", "j-1"}, "", func(f *fakeManager) {
			f.on("POST", "/api/jobs/j-1/cancel", fixtureJob)
		}, []wantRequest{{"POST", "/api/jobs/j-1/cancel", nil, ""}}},

		{"catalog list", []string{"catalog", "list", "--restorable-only"}, "", func(f *fakeManager) {
			f.on("GET", "/api/catalog", fixtureCatalog)
		}, []wantRequest{{"GET", "/api/catalog", map[string]string{"restorable_only": "true"}, ""}}},

		{"backup run explicit agent", []string{"backup", "run", "--policy", "p-1", "--agent", "ag-1"}, "", func(f *fakeManager) {
			f.on("POST", "/api/jobs/", fixtureJob)
		}, []wantRequest{{"POST", "/api/jobs/", nil, `{"agent_id":"ag-1","policy_id":"p-1"}`}}},
		{"backup run resolves single-agent policy", []string{"backup", "run", "--policy", "p-1"}, "", func(f *fakeManager) {
			f.on("GET", "/api/policies/p-1", fixturePolicy)
			f.on("POST", "/api/jobs/", fixtureJob)
		}, []wantRequest{
			{"GET", "/api/policies/p-1", nil, ""},
			{"POST", "/api/jobs/", nil, `{"agent_id":"ag-1","policy_id":"p-1"}`},
		}},

		{"restore plan", []string{"restore", "plan", "--kind", "file", "--snapshot", "snap-1", "--requested-by", "alice"}, "", func(f *fakeManager) {
			f.on("POST", "/api/restore/plan", fixturePlan)
		}, []wantRequest{{"POST", "/api/restore/plan", nil, `{"kind":"file","snapshot_id":"snap-1","requested_by":"alice"}`}}},
		{"restore confirm", []string{"restore", "confirm", "--plan", "plan-1", "--actor", "alice", "--yes"}, "", func(f *fakeManager) {
			f.on("POST", "/api/restore/confirm", fixtureConfirm)
		}, []wantRequest{{"POST", "/api/restore/confirm", nil, `{"plan_id":"plan-1","actor":"alice"}`}}},
		{"restore execute", []string{"restore", "execute", "--token", "tok", "--actor", "alice", "--yes"}, "", func(f *fakeManager) {
			f.on("POST", "/api/restore/execute", fixtureRestore)
		}, []wantRequest{{"POST", "/api/restore/execute", nil, `{"token":"tok","actor":"alice"}`}}},
		{"restore status", []string{"restore", "status", "--job", "rj-1"}, "", func(f *fakeManager) {
			f.on("GET", "/api/restore/status", fixtureRestore)
		}, []wantRequest{{"GET", "/api/restore/status", map[string]string{"id": "rj-1"}, ""}}},

		{"apikeys list", []string{"apikeys", "list"}, "", func(f *fakeManager) {
			f.on("GET", "/api/api-keys", fixtureKeys)
		}, []wantRequest{{"GET", "/api/api-keys", nil, ""}}},
		{"apikeys create", []string{"apikeys", "create", "--name", "ci-2", "--scopes", "read,write", "--ttl", "720h"}, "", func(f *fakeManager) {
			f.on("POST", "/api/api-keys", fixtureKeyNew)
		}, []wantRequest{{"POST", "/api/api-keys", nil, `{"name":"ci-2","scopes":["read","write"],"ttl_hours":720}`}}},
		{"apikeys revoke", []string{"apikeys", "revoke", "kid-1"}, "", func(f *fakeManager) {
			f.on("DELETE", "/api/api-keys/kid-1", `{}`)
		}, []wantRequest{{"DELETE", "/api/api-keys/kid-1", nil, ""}}},

		{"backup run multi-agent policy errors before POST", []string{"backup", "run", "--policy", "p-9"}, "", func(f *fakeManager) {
			f.on("GET", "/api/policies/p-9", policyTwoAgents)
		}, []wantRequest{{"GET", "/api/policies/p-9", nil, ""}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeManager(t)
			tc.setup(f)
			code, _, stderr := execCLI(t, f, tc.stdin, tc.args...)
			if strings.Contains(tc.name, "errors before POST") {
				if code == 0 {
					t.Fatal("expected non-zero exit")
				}
				if !strings.Contains(stderr, "--agent") {
					t.Fatalf("stderr should demand --agent, got %q", stderr)
				}
			} else if code != 0 {
				t.Fatalf("exit %d, stderr: %s", code, stderr)
			}
			assertRequestTable(t, f, tc.want)
		})
	}
}

func TestAgentsListGoldenTable(t *testing.T) {
	f := newFakeManager(t)
	f.on("GET", "/api/agents", fixtureAgents)
	code, stdout, stderr := execCLI(t, f, "", "agents", "list")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	want := "ID    HOSTNAME  STATUS   VERSION  LAST_HEARTBEAT\n" +
		"ag-1  vm-1      active   1.2.0    2026-11-01T10:00:00Z\n" +
		"ag-2  vm-2      offline  1.1.0    -\n"
	if stdout != want {
		t.Fatalf("table output mismatch:\ngot:\n%s\nwant:\n%s", stdout, want)
	}
}

func TestWhoamiPrintsUserAndRoles(t *testing.T) {
	f := newFakeManager(t)
	f.on("GET", "/api/auth/me", fixtureMe)
	code, stdout, stderr := execCLI(t, f, "", "whoami")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, sub := range []string{"Alice", "alice@example.com", "admin", "operator"} {
		if !strings.Contains(stdout, sub) {
			t.Fatalf("whoami output missing %q: %q", sub, stdout)
		}
	}
}

func TestOutputJSONPassesThrough(t *testing.T) {
	f := newFakeManager(t)
	f.on("GET", "/api/agents", fixtureAgents)
	code, stdout, _ := execCLI(t, f, "", "--output", "json", "agents", "list")
	if code != 0 {
		t.Fatal("expected success")
	}
	var v struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.Unmarshal([]byte(stdout), &v); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if v.Total != 2 || len(v.Items) != 2 {
		t.Fatalf("unexpected decoded body: %+v", v)
	}
}

func TestInvalidOutputFlag(t *testing.T) {
	f := newFakeManager(t)
	f.on("GET", "/api/agents", fixtureAgents)
	code, _, stderr := execCLI(t, f, "", "--output", "yaml", "agents", "list")
	if code == 0 {
		t.Fatal("expected non-zero exit")
	}
	if !strings.Contains(stderr, "--output") {
		t.Fatalf("stderr should name --output, got %q", stderr)
	}
}

func TestAPIErrorExitNonZeroWithMessage(t *testing.T) {
	f := newFakeManager(t)
	f.onStatus("GET", "/api/agents/ag-9", http.StatusNotFound,
		`{"error":{"code":404,"message":"agent ag-9 not found"}}`)
	code, _, stderr := execCLI(t, f, "", "agents", "show", "ag-9")
	if code == 0 {
		t.Fatal("expected non-zero exit")
	}
	if !strings.Contains(stderr, "agent ag-9 not found") {
		t.Fatalf("stderr should carry the server message, got %q", stderr)
	}
}

func Test401HintsLoginEndToEnd(t *testing.T) {
	f := newFakeManager(t)
	f.onStatus("GET", "/api/agents", http.StatusUnauthorized,
		`{"error":{"code":401,"message":"bad key"}}`)
	code, _, stderr := execCLI(t, f, "", "agents", "list")
	if code == 0 {
		t.Fatal("expected non-zero exit")
	}
	if !strings.Contains(stderr, "vaultix-cli login") {
		t.Fatalf("401 should hint at login, got %q", stderr)
	}
}

func TestNotLoggedInError(t *testing.T) {
	f := newFakeManager(t)
	// Unset the key env (execCLI sets it) by running run() directly with a
	// fresh config path and only the manager URL in the environment.
	t.Setenv(envManagerURL, f.url())
	oldKey, hadKey := os.LookupEnv(envAPIKey)
	if err := os.Unsetenv(envAPIKey); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if hadKey {
			_ = os.Setenv(envAPIKey, oldKey)
		}
	})
	cfg := filepath.Join(t.TempDir(), "cli.yaml")
	var out, errB bytes.Buffer
	code := run([]string{"--config", cfg, "agents", "list"}, strings.NewReader(""), &out, &errB)
	if code == 0 {
		t.Fatal("expected non-zero exit")
	}
	if !strings.Contains(errB.String(), "vaultix-cli login") {
		t.Fatalf("missing key should hint at login, got %q", errB.String())
	}
	if len(f.requests) != 0 {
		t.Fatalf("no request should be sent without a key: %+v", f.requests)
	}
}

func TestDebugEndToEndRedactsKey(t *testing.T) {
	f := newFakeManager(t)
	f.on("GET", "/api/auth/me", fixtureMe)
	code, stdout, stderr := execCLI(t, f, "", "--debug", "whoami")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(stderr, "debug: GET") || !strings.Contains(stderr, "/api/auth/me") {
		t.Fatalf("stderr should carry the debug line, got %q", stderr)
	}
	if combined := stdout + stderr; strings.Contains(combined, "vxk_") {
		t.Fatalf("output leaks the key: %q", combined)
	}
}

func TestLoginLogoutRoundTrip(t *testing.T) {
	f := newFakeManager(t)
	f.on("GET", "/api/auth/me", fixtureMe)
	cfg := filepath.Join(t.TempDir(), "vaultix", "cli.yaml")
	t.Setenv(envManagerURL, "")
	t.Setenv(envAPIKey, "")

	var out, errB bytes.Buffer
	code := run([]string{"--config", cfg, "login", "--manager", f.url(), "--api-key", "vxk_login_key"},
		strings.NewReader(""), &out, &errB)
	if code != 0 {
		t.Fatalf("login exit %d: %s", code, errB.String())
	}
	if combined := out.String() + errB.String(); strings.Contains(combined, "vxk_login_key") {
		t.Fatalf("login output must never echo the key: %q", combined)
	}
	if !strings.Contains(out.String(), "Alice") {
		t.Fatalf("login should greet the verified user: %q", out.String())
	}
	st, err := os.Stat(cfg)
	if err != nil {
		t.Fatalf("login should create config: %v", err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Fatalf("config perms = %o, want 600", perm)
	}
	saved, err := loadFileConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ManagerURL != f.url() || saved.APIKey != "vxk_login_key" {
		t.Fatalf("saved config = %+v", saved)
	}

	out.Reset()
	errB.Reset()
	code = run([]string{"--config", cfg, "logout"}, strings.NewReader(""), &out, &errB)
	if code != 0 {
		t.Fatalf("logout exit %d: %s", code, errB.String())
	}
	saved, err = loadFileConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if saved.APIKey != "" {
		t.Fatalf("logout should clear the key: %+v", saved)
	}
	if saved.ManagerURL != f.url() {
		t.Fatalf("logout should keep the manager URL: %+v", saved)
	}
}

func TestLoginFailureStoresNothing(t *testing.T) {
	f := newFakeManager(t)
	f.onStatus("GET", "/api/auth/me", http.StatusUnauthorized, `{"error":{"code":401,"message":"bad key"}}`)
	cfg := filepath.Join(t.TempDir(), "cli.yaml")
	t.Setenv(envManagerURL, "")
	t.Setenv(envAPIKey, "")
	var out, errB bytes.Buffer
	code := run([]string{"--config", cfg, "login", "--manager", f.url(), "--api-key", "vxk_bad"},
		strings.NewReader(""), &out, &errB)
	if code == 0 {
		t.Fatal("expected non-zero exit")
	}
	if _, err := os.Stat(cfg); !os.IsNotExist(err) {
		t.Fatal("failed login must not create config")
	}
}

func TestLoginMissingKeyNonTTYErrors(t *testing.T) {
	f := newFakeManager(t)
	cfg := filepath.Join(t.TempDir(), "cli.yaml")
	t.Setenv(envManagerURL, f.url())
	oldKey, hadKey := os.LookupEnv(envAPIKey)
	if err := os.Unsetenv(envAPIKey); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if hadKey {
			_ = os.Setenv(envAPIKey, oldKey)
		}
	})
	var out, errB bytes.Buffer
	// Piped (non-terminal) stdin: must error, never hang on a prompt.
	code := run([]string{"--config", cfg, "login"}, strings.NewReader(""), &out, &errB)
	if code == 0 {
		t.Fatal("expected non-zero exit")
	}
	if !strings.Contains(errB.String(), "--api-key") {
		t.Fatalf("stderr should demand --api-key, got %q", errB.String())
	}
	if len(f.requests) != 0 {
		t.Fatalf("no request should be sent: %+v", f.requests)
	}
}

func TestRestoreConfirmTypedAcceptsAndRejects(t *testing.T) {
	f := newFakeManager(t)
	f.on("POST", "/api/restore/confirm", fixtureConfirm)

	code, stdout, stderr := execCLI(t, f, "plan-1\n", "restore", "confirm", "--plan", "plan-1", "--actor", "alice")
	if code != 0 {
		t.Fatalf("typed confirm exit %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "confirm-tok-1") {
		t.Fatalf("confirm should print the token: %q", stdout)
	}
	assertRequestTable(t, f, []wantRequest{{"POST", "/api/restore/confirm", nil, `{"plan_id":"plan-1","actor":"alice"}`}})

	f2 := newFakeManager(t)
	f2.on("POST", "/api/restore/confirm", fixtureConfirm)
	code, _, stderr = execCLI(t, f2, "wrong-id\n", "restore", "confirm", "--plan", "plan-1", "--actor", "alice")
	if code == 0 {
		t.Fatal("mismatched typed confirm should fail")
	}
	if !strings.Contains(stderr, "mismatch") {
		t.Fatalf("stderr should report mismatch, got %q", stderr)
	}
	if len(f2.requests) != 0 {
		t.Fatalf("rejected confirm must send no request: %+v", f2.requests)
	}
}

func TestRestoreExecuteRequiresYesOrEXECUTE(t *testing.T) {
	f := newFakeManager(t)
	f.on("POST", "/api/restore/execute", fixtureRestore)
	code, _, _ := execCLI(t, f, "EXECUTE\n", "restore", "execute", "--token", "tok", "--actor", "alice")
	if code != 0 {
		t.Fatal("typed EXECUTE should succeed")
	}
	assertRequestTable(t, f, []wantRequest{{"POST", "/api/restore/execute", nil, `{"token":"tok","actor":"alice"}`}})

	f2 := newFakeManager(t)
	f2.on("POST", "/api/restore/execute", fixtureRestore)
	code, _, stderr := execCLI(t, f2, "", "restore", "execute", "--token", "tok", "--actor", "alice")
	if code == 0 {
		t.Fatal("EOF stdin without --yes should fail")
	}
	if !strings.Contains(stderr, "--yes") {
		t.Fatalf("stderr should mention --yes, got %q", stderr)
	}
	if len(f2.requests) != 0 {
		t.Fatalf("unconfirmed execute must send no request: %+v", f2.requests)
	}
}

func TestAPIKeysCreatePrintsKeyOnceWithWarning(t *testing.T) {
	f := newFakeManager(t)
	f.on("POST", "/api/api-keys", fixtureKeyNew)
	code, stdout, stderr := execCLI(t, f, "", "apikeys", "create", "--name", "ci-2")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if n := strings.Count(stdout, "vxk_shown_once_abc123"); n != 1 {
		t.Fatalf("key should print exactly once, got %d:\n%s", n, stdout)
	}
	if !strings.Contains(stdout, "ONCE") {
		t.Fatalf("create should warn loudly:\n%s", stdout)
	}
}

func TestAPIKeysCreateRejectsBadTTL(t *testing.T) {
	f := newFakeManager(t)
	f.on("POST", "/api/api-keys", fixtureKeyNew)
	code, _, stderr := execCLI(t, f, "", "apikeys", "create", "--name", "ci-2", "--ttl", "not-a-duration")
	if code == 0 {
		t.Fatal("bad --ttl should fail")
	}
	if !strings.Contains(stderr, "invalid --ttl") {
		t.Fatalf("stderr should name --ttl, got %q", stderr)
	}
	if len(f.requests) != 0 {
		t.Fatalf("bad --ttl must send no request: %+v", f.requests)
	}
}

func TestUnknownCommandExitsNonZeroWithUsage(t *testing.T) {
	var out, errB bytes.Buffer
	code := run([]string{"frobnicate"}, strings.NewReader(""), &out, &errB)
	if code == 0 {
		t.Fatal("expected non-zero exit")
	}
	if !strings.Contains(errB.String(), "unknown command") {
		t.Fatalf("stderr should name the problem, got %q", errB.String())
	}
	if !strings.Contains(errB.String(), "Usage:") {
		t.Fatalf("stderr should include usage, got %q", errB.String())
	}
}

func TestUnknownFlagExitsNonZeroWithUsage(t *testing.T) {
	var out, errB bytes.Buffer
	code := run([]string{"agents", "list", "--bogus"}, strings.NewReader(""), &out, &errB)
	if code == 0 {
		t.Fatal("expected non-zero exit")
	}
	if !strings.Contains(errB.String(), "unknown flag") {
		t.Fatalf("stderr should name the problem, got %q", errB.String())
	}
	if !strings.Contains(errB.String(), "Usage:") {
		t.Fatalf("stderr should include usage, got %q", errB.String())
	}
}

func TestEndpointTablePinned(t *testing.T) {
	want := map[string]endpoint{
		"auth.me":         {mGet, "/api/auth/me"},
		"agents.list":     {mGet, "/api/agents"},
		"agents.show":     {mGet, "/api/agents/{id}"},
		"policies.list":   {mGet, "/api/policies"},
		"policies.show":   {mGet, "/api/policies/{id}"},
		"jobs.list":       {mGet, "/api/jobs/"},
		"jobs.create":     {mPost, "/api/jobs/"},
		"jobs.show":       {mGet, "/api/jobs/{id}"},
		"jobs.retry":      {mPost, "/api/jobs/{id}/retry"},
		"jobs.cancel":     {mPost, "/api/jobs/{id}/cancel"},
		"catalog.list":    {mGet, "/api/catalog"},
		"restore.plan":    {mPost, "/api/restore/plan"},
		"restore.confirm": {mPost, "/api/restore/confirm"},
		"restore.execute": {mPost, "/api/restore/execute"},
		"restore.status":  {mGet, "/api/restore/status"},
		"apikeys.list":    {mGet, "/api/api-keys"},
		"apikeys.create":  {mPost, "/api/api-keys"},
		"apikeys.revoke":  {mDelete, "/api/api-keys/{id}"},
	}
	if !reflect.DeepEqual(endpoints, want) {
		t.Fatalf("endpoint table drifted: got %+v", endpoints)
	}
	if _, _, err := expandEndpoint("agents.show", nil); err == nil {
		t.Fatal("missing path args should error")
	}
	if _, _, err := expandEndpoint("nope", nil); err == nil {
		t.Fatal("unknown endpoint should error")
	}
}
