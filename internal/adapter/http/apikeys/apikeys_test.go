package apikeys

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
	apikeysvc "github.com/vaultix/vaultix/internal/usecase/apikey"
)

var errBoom = errors.New("boom")

type fakeService struct {
	issued    *apikeysvc.IssuedKey
	issueErr  error
	listed    []domain.ApiKey
	listErr   error
	revokeErr error
	actorSeen []string
}

func (f *fakeService) Issue(_ context.Context, userID, name string, scopes []string, ttl time.Duration) (*apikeysvc.IssuedKey, error) {
	f.actorSeen = append(f.actorSeen, userID)
	if f.issueErr != nil {
		return nil, f.issueErr
	}
	if f.issued != nil {
		return f.issued, nil
	}
	now := time.Now()
	return &apikeysvc.IssuedKey{
		Key:    &domain.ApiKey{ID: "k1", UserID: userID, Name: name, KeyPrefix: "vxk_test1", Scopes: scopes, CreatedAt: now},
		Secret: "vxk_test00000000000000000000000001",
	}, nil
}

func (f *fakeService) List(_ context.Context, userID string) ([]domain.ApiKey, error) {
	f.actorSeen = append(f.actorSeen, userID)
	return f.listed, f.listErr
}

func (f *fakeService) Revoke(_ context.Context, actorID, keyID string) error {
	f.actorSeen = append(f.actorSeen, actorID+":"+keyID)
	return f.revokeErr
}

var _ Service = (*fakeService)(nil)

type fakeRouter struct {
	routes map[string]http.HandlerFunc
}

func newFakeRouter() *fakeRouter { return &fakeRouter{routes: map[string]http.HandlerFunc{}} }

func (f *fakeRouter) Get(p string, h http.HandlerFunc)    { f.routes["GET "+p] = h }
func (f *fakeRouter) Post(p string, h http.HandlerFunc)   { f.routes["POST "+p] = h }
func (f *fakeRouter) Put(p string, h http.HandlerFunc)    { f.routes["PUT "+p] = h }
func (f *fakeRouter) Patch(p string, h http.HandlerFunc)  { f.routes["PATCH "+p] = h }
func (f *fakeRouter) Delete(p string, h http.HandlerFunc) { f.routes["DELETE "+p] = h }

var _ Router = (*fakeRouter)(nil)

func (f *fakeRouter) call(method, pattern string) http.HandlerFunc {
	h, ok := f.routes[method+" "+pattern]
	if !ok {
		panic("route not registered: " + method + " " + pattern)
	}
	return h
}

func authed(actor string) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc { return next }
}

func actorOf(actor string, ok bool) func(*http.Request) (string, bool) {
	return func(*http.Request) (string, bool) { return actor, ok }
}

func TestMountRegistersCRUD(t *testing.T) {
	r := newFakeRouter()
	Mount(r, Deps{Service: &fakeService{}, Session: authed("u1"), Actor: actorOf("u1", true)})
	for _, route := range []string{"GET /api/api-keys", "POST /api/api-keys", "DELETE /api/api-keys/{id}"} {
		if _, ok := r.routes[route]; !ok {
			t.Fatalf("route missing: %s", route)
		}
	}
}

func TestListMetadataOnly(t *testing.T) {
	now := time.Now()
	svc := &fakeService{listed: []domain.ApiKey{
		{ID: "k1", UserID: "u1", Name: "cli", KeyPrefix: "vxk_test1", Scopes: []string{"read"}, CreatedAt: now},
	}}
	r := newFakeRouter()
	Mount(r, Deps{Service: svc, Session: authed("u1"), Actor: actorOf("u1", true)})
	rec := httptest.NewRecorder()
	r.call("GET", "/api/api-keys")(rec, httptest.NewRequest("GET", "/api/api-keys", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "vxk_") && !strings.Contains(body, "vxk_test1") {
		t.Fatalf("list leaked secret material: %s", body)
	}
	if strings.Contains(body, "key_hash") || strings.Contains(body, "\"key\"") {
		t.Fatalf("list must never return secrets or hashes: %s", body)
	}
	var got struct {
		Items []keyJSON `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 || got.Items[0].ID != "k1" || got.Items[0].KeyPrefix != "vxk_test1" {
		t.Fatalf("items = %+v", got.Items)
	}
}

func TestIssueReturnsSecretOnce(t *testing.T) {
	svc := &fakeService{}
	r := newFakeRouter()
	Mount(r, Deps{Service: svc, Session: authed("u1"), Actor: actorOf("u1", true)})
	req := httptest.NewRequest("POST", "/api/api-keys",
		bytes.NewBufferString(`{"name":"cli","scopes":["read"],"ttl_hours":24}`))
	rec := httptest.NewRecorder()
	r.call("POST", "/api/api-keys")(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("code = %d: %s", rec.Code, rec.Body.String())
	}
	var got issueResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Key == "" || !strings.HasPrefix(got.Key, "vxk_") {
		t.Fatalf("secret missing: %+v", got)
	}
	if got.ID != "k1" {
		t.Fatalf("metadata missing: %+v", got)
	}
}

func TestIssueValidation(t *testing.T) {
	svc := &fakeService{}
	r := newFakeRouter()
	Mount(r, Deps{Service: svc, Session: authed("u1"), Actor: actorOf("u1", true)})
	for _, body := range []string{
		`{"scopes":["read"]}`,
		`{"name":"k"}`,
		`{"name":"k","scopes":["read"],"ttl_hours":-1}`,
		`{"name":"k","scopes":["read"],"ttl_hours":9000}`,
		`not json`,
	} {
		req := httptest.NewRequest("POST", "/api/api-keys", bytes.NewBufferString(body))
		rec := httptest.NewRecorder()
		r.call("POST", "/api/api-keys")(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %q: code = %d", body, rec.Code)
		}
	}
}

func TestRevoke(t *testing.T) {
	svc := &fakeService{}
	r := newFakeRouter()
	Mount(r, Deps{Service: svc, Session: authed("u1"), Actor: actorOf("u1", true)})
	req := httptest.NewRequest("DELETE", "/api/api-keys/k9", nil)
	req.SetPathValue("id", "k9")
	rec := httptest.NewRecorder()
	r.call("DELETE", "/api/api-keys/{id}")(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code = %d", rec.Code)
	}
	if len(svc.actorSeen) != 1 || svc.actorSeen[0] != "u1:k9" {
		t.Fatalf("actor/key = %v", svc.actorSeen)
	}
	// Raw-path fallback (chi bridge absent).
	req2 := httptest.NewRequest("DELETE", "/api/api-keys/k7", nil)
	rec2 := httptest.NewRecorder()
	r.call("DELETE", "/api/api-keys/{id}")(rec2, req2)
	if rec2.Code != http.StatusNoContent {
		t.Fatalf("raw path code = %d", rec2.Code)
	}
}

func TestUnauthenticated401(t *testing.T) {
	r := newFakeRouter()
	Mount(r, Deps{Service: &fakeService{}, Actor: actorOf("", false)})
	rec := httptest.NewRecorder()
	r.call("GET", "/api/api-keys")(rec, httptest.NewRequest("GET", "/api/api-keys", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d", rec.Code)
	}
}

func TestErrorMapping(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{domain.ErrValidation, http.StatusBadRequest},
		{domain.ErrForbidden, http.StatusForbidden},
		{domain.ErrNotFound, http.StatusNotFound},
		{errBoom, http.StatusInternalServerError},
	}
	for _, tc := range cases {
		r := newFakeRouter()
		Mount(r, Deps{Service: &fakeService{revokeErr: tc.err}, Actor: actorOf("u1", true)})
		req := httptest.NewRequest("DELETE", "/api/api-keys/k1", nil)
		req.SetPathValue("id", "k1")
		rec := httptest.NewRecorder()
		r.call("DELETE", "/api/api-keys/{id}")(rec, req)
		if rec.Code != tc.want {
			t.Fatalf("err %v: code = %d, want %d", tc.err, rec.Code, tc.want)
		}
	}
}

func TestTTLHours(t *testing.T) {
	if ttlHours(0) != 0 || ttlHours(-5) != 0 {
		t.Fatal("non-positive must mean no expiry")
	}
	if ttlHours(1.5) != 90*time.Minute {
		t.Fatalf("fractional hours = %v", ttlHours(1.5))
	}
}
