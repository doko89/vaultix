package notification

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vaultix/vaultix/internal/domain"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testSenderConfig(url string) WebhookConfig {
	return WebhookConfig{
		Endpoints:       map[string]WebhookEndpoint{"ops": {URL: url}},
		Timeout:         2 * time.Second,
		MaxTries:        3,
		InitialInterval: time.Millisecond,
		MaxInterval:     5 * time.Millisecond,
	}
}

func TestWebhookSendSuccess(t *testing.T) {
	var gotBody []byte
	var gotSig string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotSig = r.Header.Get("X-Vaultix-Signature")
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := testSenderConfig(srv.URL + "/hook?token=secret-token")
	cfg.Endpoints["ops"] = WebhookEndpoint{URL: srv.URL + "/hook?token=secret-token", Secret: "s3cr3t"}
	sender, err := NewWebhookSender(cfg, nil, discardLogger())
	require.NoError(t, err)

	err = sender.Send(context.Background(), &domain.Notification{
		Recipient: "ops", Subject: "job failed", Body: "details",
		RefType: "backup_job", RefID: "j-1",
	})
	require.NoError(t, err)
	assert.Contains(t, string(gotBody), "job failed")
	assert.Contains(t, string(gotBody), "backup_job")
	assert.NotEmpty(t, gotSig, "HMAC signature header must be set when a secret is configured")
	assert.Len(t, gotSig, 64)
}

func TestWebhookRetriesTransientThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sender, err := NewWebhookSender(testSenderConfig(srv.URL), nil, discardLogger())
	require.NoError(t, err)
	err = sender.Send(context.Background(), &domain.Notification{Recipient: "ops", Subject: "s"})
	require.NoError(t, err)
	assert.Equal(t, int32(3), calls.Load())
}

func TestWebhookGivesUpAfterBudget(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := testSenderConfig(srv.URL)
	cfg.MaxTries = 2
	sender, err := NewWebhookSender(cfg, nil, discardLogger())
	require.NoError(t, err)
	err = sender.Send(context.Background(), &domain.Notification{Recipient: "ops", Subject: "s"})
	require.Error(t, err)
	assert.Equal(t, int32(2), calls.Load(), "must stop after the retry budget")
}

func TestWebhookNoRetryOnClientError(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	sender, err := NewWebhookSender(testSenderConfig(srv.URL), nil, discardLogger())
	require.NoError(t, err)
	err = sender.Send(context.Background(), &domain.Notification{Recipient: "ops", Subject: "s"})
	require.Error(t, err)
	assert.Equal(t, int32(1), calls.Load(), "4xx must fail fast without retry")
}

func TestWebhookDirectURLHeadersRawBody(t *testing.T) {
	var gotBody []byte
	var gotAuth, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sender, err := NewWebhookSender(WebhookConfig{}, nil, discardLogger())
	require.NoError(t, err)

	err = sender.Send(context.Background(), &domain.Notification{
		Recipient: srv.URL + "/hook",
		Subject:   "job failed",
		Body:      `{"text":"custom"}`,
		RawBody:   true,
		Headers:   map[string]string{"Authorization": "Bearer abc"},
	})
	require.NoError(t, err)
	assert.Equal(t, `{"text":"custom"}`, string(gotBody))
	assert.Equal(t, "Bearer abc", gotAuth)
	assert.Equal(t, "application/json", gotCT)
}

func TestWebhookRuleHeadersOverrideEndpoint(t *testing.T) {
	var gotCustom, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCustom = r.Header.Get("X-Custom")
		gotCT = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := testSenderConfig(srv.URL + "/hook")
	cfg.Endpoints["ops"] = WebhookEndpoint{URL: srv.URL + "/hook", Headers: map[string]string{"X-Custom": "endpoint"}}
	sender, err := NewWebhookSender(cfg, nil, discardLogger())
	require.NoError(t, err)

	err = sender.Send(context.Background(), &domain.Notification{
		Recipient: "ops", Body: "x",
		Headers: map[string]string{"X-Custom": "rule", "Content-Type": "text/plain"},
	})
	require.NoError(t, err)
	assert.Equal(t, "rule", gotCustom)
	assert.Equal(t, "text/plain", gotCT)
}

func TestWebhookUnknownEndpoint(t *testing.T) {
	sender, err := NewWebhookSender(testSenderConfig("http://example.invalid"), nil, discardLogger())
	require.NoError(t, err)
	err = sender.Send(context.Background(), &domain.Notification{Recipient: "nope"})
	require.ErrorContains(t, err, `unknown endpoint "nope"`)
}

func TestWebhookSecretNeverLogged(t *testing.T) {
	// A failing endpoint whose URL carries a token: the returned error (and
	// anything logged from it) must not contain the token or query string.
	// Port 1 refuses connections, and the transport error embeds the URL.
	raw := "http://127.0.0.1:1/hook?token=super-secret-token&sig=abc"
	cfg := testSenderConfig(raw)
	cfg.MaxTries = 1
	sender, err := NewWebhookSender(cfg, nil, discardLogger())
	require.NoError(t, err)
	err = sender.Send(context.Background(), &domain.Notification{Recipient: "ops", Subject: "s"})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "super-secret-token")
	assert.NotContains(t, err.Error(), "token=")
	// The redacted host form is safe to keep for debuggability.
	assert.Contains(t, err.Error(), "/hook")
}

func TestNewWebhookSenderRejectsBadURL(t *testing.T) {
	_, err := NewWebhookSender(WebhookConfig{Endpoints: map[string]WebhookEndpoint{
		"bad": {URL: "notaurl"},
	}}, nil, discardLogger())
	require.Error(t, err)
}

func TestWebhookTestPath(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sender, err := NewWebhookSender(WebhookConfig{}, nil, discardLogger())
	require.NoError(t, err)
	require.NoError(t, sender.Test(context.Background(), srv.URL+"/x", "hello"))
	assert.Equal(t, int32(1), calls.Load())

	err = sender.Test(context.Background(), "notaurl", "hello")
	assert.ErrorIs(t, err, domain.ErrValidation)
}

// ---- service-level fanout with fakes ----

type fakeOutbox struct {
	mu     sync.Mutex
	rows   []*domain.Notification
	nextID int
}

func (f *fakeOutbox) CreateNotification(_ context.Context, n *domain.Notification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	n.ID = fmt.Sprintf("n-%d", f.nextID)
	cp := *n
	f.rows = append(f.rows, &cp)
	*n = cp
	return nil
}

func (f *fakeOutbox) UpdateNotification(_ context.Context, n *domain.Notification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, r := range f.rows {
		if r.ID == n.ID {
			cp := *n
			f.rows[i] = &cp
			return nil
		}
	}
	return fmt.Errorf("not found")
}

func (f *fakeOutbox) ListPending(_ context.Context, limit int) ([]domain.Notification, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Notification
	for _, r := range f.rows {
		if r.Status == domain.NotificationPending {
			out = append(out, *r)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

type stubSender struct {
	channel domain.NotificationChannel
	err     error
	sent    []*domain.Notification
}

func (s *stubSender) Channel() domain.NotificationChannel { return s.channel }
func (s *stubSender) Send(_ context.Context, n *domain.Notification) error {
	cp := *n
	s.sent = append(s.sent, &cp)
	return s.err
}

func testService(t *testing.T, rules []Rule, senders ...Sender) (*Service, *fakeOutbox) {
	t.Helper()
	store, err := NewStaticRuleStore(rules)
	require.NoError(t, err)
	outbox := &fakeOutbox{}
	svc, err := New(Deps{Rules: store, Outbox: outbox, Senders: senders, Logger: discardLogger()})
	require.NoError(t, err)
	return svc, outbox
}

func TestNotifyJobFailedFansOut(t *testing.T) {
	web := &stubSender{channel: domain.NotificationWebhook}
	mail := &stubSender{channel: domain.NotificationEmail}
	svc, outbox := testService(t, []Rule{
		{Name: "ops-webhook", Events: []string{EventJobFailed}, Channel: domain.NotificationWebhook, Recipient: "ops", Enabled: true},
		{Name: "ops-mail", Events: []string{EventJobFailed}, Channel: domain.NotificationEmail, Recipient: "ops@example.com", Enabled: true},
		{Name: "disabled", Events: []string{EventJobFailed}, Channel: domain.NotificationEmail, Recipient: "x@example.com", Enabled: false},
		{Name: "other-event", Events: []string{EventAgentOffline}, Channel: domain.NotificationEmail, Recipient: "y@example.com", Enabled: true},
	}, web, mail)

	matched, err := svc.NotifyJobFailed(context.Background(), JobFailedEvent{
		JobID: "job-123456789", AgentID: "a-1", PolicyID: "p-1",
		ErrorCode: "UPLOAD_FAILED", ErrorMessage: "boom", Attempt: 2,
	})
	require.NoError(t, err)
	assert.Equal(t, 2, matched)
	assert.Len(t, web.sent, 1)
	assert.Len(t, mail.sent, 1)
	assert.Contains(t, web.sent[0].Subject, "failed")
	assert.Contains(t, web.sent[0].Body, "UPLOAD_FAILED")
	require.Len(t, outbox.rows, 2)
	for _, r := range outbox.rows {
		assert.Equal(t, domain.NotificationSent, r.Status)
		assert.NotNil(t, r.SentAt)
	}
}

func TestFanoutRendersTemplateAndHeaders(t *testing.T) {
	web := &stubSender{channel: domain.NotificationWebhook}
	svc, outbox := testService(t, []Rule{
		{
			Name: "templated", Events: []string{EventJobFailed},
			Channel: domain.NotificationWebhook, Recipient: "https://hooks.example/x",
			Headers:      map[string]string{"Authorization": "Bearer t"},
			BodyTemplate: `{"text":"{{.event}} {{.ref_id}}"}`,
			Enabled:      true,
		},
	}, web)
	_, err := svc.NotifyJobFailed(context.Background(), JobFailedEvent{
		JobID: "job-1", AgentID: "a-1", PolicyID: "p-1",
		ErrorCode: "E", ErrorMessage: "boom", Attempt: 1,
	})
	require.NoError(t, err)
	require.Len(t, web.sent, 1)
	assert.Equal(t, `{"text":"job_failed job-1"}`, web.sent[0].Body)
	assert.True(t, web.sent[0].RawBody)
	assert.Equal(t, "Bearer t", web.sent[0].Headers["Authorization"])
	require.Len(t, outbox.rows, 1)
	assert.True(t, outbox.rows[0].RawBody)
	assert.Equal(t, `{"text":"job_failed job-1"}`, outbox.rows[0].Body)
}

func TestNotifyJobFailedMarksDeliveryFailure(t *testing.T) {
	web := &stubSender{channel: domain.NotificationWebhook, err: fmt.Errorf("connection refused")}
	svc, outbox := testService(t, []Rule{
		{Name: "ops", Events: []string{EventJobFailed}, Channel: domain.NotificationWebhook, Recipient: "ops", Enabled: true},
	}, web)

	matched, err := svc.NotifyJobFailed(context.Background(), JobFailedEvent{JobID: "j-1"})
	require.NoError(t, err, "delivery failure must not fail the call")
	assert.Equal(t, 1, matched)
	require.Len(t, outbox.rows, 1)
	assert.Equal(t, domain.NotificationFailed, outbox.rows[0].Status)
	assert.Contains(t, outbox.rows[0].LastError, "connection refused")
	assert.Equal(t, 1, outbox.rows[0].Attempts)
}

func TestNotifyAgentTransitions(t *testing.T) {
	web := &stubSender{channel: domain.NotificationWebhook}
	svc, _ := testService(t, []Rule{
		{Name: "all", Events: []string{EventAgentOffline, EventAgentStale, EventAgentOnline}, Channel: domain.NotificationWebhook, Recipient: "ops", Enabled: true},
	}, web)
	ctx := context.Background()

	cases := []struct {
		from, to domain.AgentStatus
		want     int
	}{
		{domain.AgentStatusOnline, domain.AgentStatusOffline, 1},
		{domain.AgentStatusOnline, domain.AgentStatusStale, 1},
		{domain.AgentStatusOffline, domain.AgentStatusOnline, 1},
		{domain.AgentStatusStale, domain.AgentStatusOnline, 1},
		{domain.AgentStatusOnline, domain.AgentStatusOnline, 0},
		{domain.AgentStatusOffline, domain.AgentStatusOffline, 0},
		{domain.AgentStatusOnline, domain.AgentStatusRevoked, 0},
	}
	for _, c := range cases {
		web.sent = nil
		matched, err := svc.NotifyAgentTransition(ctx, AgentTransitionEvent{
			AgentID: "a-1", Hostname: "vm-1", From: c.from, To: c.to,
		})
		require.NoError(t, err)
		assert.Equal(t, c.want, matched, "from %s to %s", c.from, c.to)
		assert.Len(t, web.sent, c.want)
	}
}

func TestDrainPending(t *testing.T) {
	mail := &stubSender{channel: domain.NotificationEmail}
	svc, outbox := testService(t, nil, mail)
	ctx := context.Background()
	outbox.rows = append(outbox.rows,
		&domain.Notification{ID: "n-1", Channel: domain.NotificationEmail, Recipient: "a@example.com", Status: domain.NotificationPending},
		&domain.Notification{ID: "n-2", Channel: domain.NotificationWebhook, Recipient: "ops", Status: domain.NotificationPending},
	)
	delivered, err := svc.DrainPending(ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, delivered, "only the email row has a sender")
	assert.Len(t, mail.sent, 1)
}

func TestRuleCRUDAndValidation(t *testing.T) {
	mail := &stubSender{channel: domain.NotificationEmail}
	svc, _ := testService(t, nil, mail)
	ctx := context.Background()

	_, err := svc.CreateRule(ctx, Rule{Name: "", Events: []string{EventJobFailed}, Channel: domain.NotificationEmail, Recipient: "a@example.com"})
	assert.ErrorIs(t, err, domain.ErrValidation)

	_, err = svc.CreateRule(ctx, Rule{Name: "bad", Events: []string{"nope"}, Channel: domain.NotificationEmail, Recipient: "a@example.com"})
	assert.ErrorIs(t, err, domain.ErrValidation)

	created, err := svc.CreateRule(ctx, Rule{Name: "ops", Events: []string{EventJobFailed}, Channel: domain.NotificationEmail, Recipient: "a@example.com", Enabled: true})
	require.NoError(t, err)
	require.NotEmpty(t, created.ID)

	got, err := svc.GetRule(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, "ops", got.Name)

	updated, err := svc.UpdateRule(ctx, created.ID, Rule{Name: "ops2", Events: []string{EventJobFailed}, Channel: domain.NotificationEmail, Recipient: "b@example.com", Enabled: true})
	require.NoError(t, err)
	assert.Equal(t, "ops2", updated.Name)

	rules, err := svc.ListRules(ctx)
	require.NoError(t, err)
	require.Len(t, rules, 1)

	require.NoError(t, svc.TestRule(ctx, created.ID))
	require.Len(t, mail.sent, 1)
	assert.Contains(t, mail.sent[0].Subject, "test")

	require.NoError(t, svc.DeleteRule(ctx, created.ID))
	_, err = svc.GetRule(ctx, created.ID)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestNewValidation(t *testing.T) {
	store, _ := NewStaticRuleStore(nil)
	_, err := New(Deps{Outbox: &fakeOutbox{}, Senders: []Sender{&stubSender{channel: domain.NotificationEmail}}})
	assert.Error(t, err) // missing rules
	_, err = New(Deps{Rules: store, Senders: []Sender{&stubSender{channel: domain.NotificationEmail}}})
	assert.Error(t, err) // missing outbox
	_, err = New(Deps{Rules: store, Outbox: &fakeOutbox{}})
	assert.Error(t, err) // missing senders
}
