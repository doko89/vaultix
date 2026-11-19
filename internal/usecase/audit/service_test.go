package audit

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vaultix/vaultix/internal/domain"
)

type fakeRepo struct {
	mu      sync.Mutex
	entries []domain.AuditLog
}

func (f *fakeRepo) Append(_ context.Context, e *domain.AuditLog) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *e
	cp.ID = string(rune('a' + len(f.entries)))
	f.entries = append(f.entries, cp)
	*e = cp
	return nil
}

func (f *fakeRepo) List(_ context.Context, opts domain.ListOptions, filter domain.AuditFilter) (domain.ListResult[domain.AuditLog], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var matched []domain.AuditLog
	for _, e := range f.entries {
		if filter.ActorID != "" && e.ActorID != filter.ActorID {
			continue
		}
		if filter.Action != "" && e.Action != filter.Action {
			continue
		}
		if filter.Since != nil && e.CreatedAt.Before(*filter.Since) {
			continue
		}
		if filter.Until != nil && !e.CreatedAt.Before(*filter.Until) {
			continue
		}
		matched = append(matched, e)
	}
	total := len(matched)
	if opts.Offset < len(matched) {
		matched = matched[opts.Offset:]
	} else {
		matched = nil
	}
	if opts.Limit > 0 && opts.Limit < len(matched) {
		matched = matched[:opts.Limit]
	}
	return domain.ListResult[domain.AuditLog]{Items: matched, Total: total}, nil
}

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

func TestAppendSetsTimestampAndPersists(t *testing.T) {
	repo := &fakeRepo{}
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	svc, err := New(repo, fixedClock{t: now})
	require.NoError(t, err)

	got, err := svc.Append(context.Background(), Entry{
		ActorType:  "user",
		ActorID:    "u-1",
		Action:     domain.AuditJobStart,
		TargetType: "backup_job",
		TargetID:   "j-1",
		Outcome:    OutcomeOK,
		Detail:     "started backup",
		IPAddress:  "10.0.0.1",
	})
	require.NoError(t, err)
	assert.Equal(t, now, got.CreatedAt)
	assert.Equal(t, "u-1", got.ActorID)
	assert.Equal(t, domain.AuditJobStart, got.Action)
	require.Len(t, repo.entries, 1)
}

func TestAppendDefaultsActorAndOutcome(t *testing.T) {
	repo := &fakeRepo{}
	svc, err := New(repo, nil) // nil clock -> system clock
	require.NoError(t, err)
	got, err := svc.Append(context.Background(), Entry{Action: domain.AuditLogin})
	require.NoError(t, err)
	assert.Equal(t, "system", got.ActorType)
	assert.Equal(t, OutcomeOK, got.Outcome)
	assert.False(t, got.CreatedAt.IsZero())
}

func TestAppendValidation(t *testing.T) {
	repo := &fakeRepo{}
	svc, err := New(repo, nil)
	require.NoError(t, err)
	ctx := context.Background()

	_, err = New(nil, nil)
	require.Error(t, err)

	_, err = svc.Append(ctx, Entry{})
	assert.ErrorIs(t, err, domain.ErrValidation) // missing action

	_, err = svc.Append(ctx, Entry{Action: domain.AuditLogin, ActorType: "ghost"})
	assert.ErrorIs(t, err, domain.ErrValidation)

	_, err = svc.Append(ctx, Entry{Action: domain.AuditLogin, Outcome: "maybe"})
	assert.ErrorIs(t, err, domain.ErrValidation)
}

func TestAppendScrubsSecrets(t *testing.T) {
	repo := &fakeRepo{}
	svc, err := New(repo, nil)
	require.NoError(t, err)

	secrets := []string{
		`login attempt password=hunter2 user=bob`,
		`oidc callback token=eyJhbGciOiJIUzI1NiJ9 user=sub-1`,
		`{"client_secret": "shh-top-secret", "user": "u-1"}`,
		`storage update api_key=AKIAIOSFODNN7EXAMPLE ok`,
		`Authorization: Bearer abcdef123456`,
	}
	for _, detail := range secrets {
		_, err := svc.Append(context.Background(), Entry{Action: domain.AuditLoginFailed, Detail: detail})
		require.NoError(t, err)
	}
	for _, e := range repo.entries {
		assert.NotContains(t, e.Detail, "hunter2")
		assert.NotContains(t, e.Detail, "eyJhbGciOiJIUzI1NiJ9")
		assert.NotContains(t, e.Detail, "shh-top-secret")
		assert.NotContains(t, e.Detail, "AKIAIOSFODNN7EXAMPLE")
		assert.NotContains(t, e.Detail, "abcdef123456")
		assert.True(t, strings.Contains(e.Detail, "[redacted]"), "expected redaction in %q", e.Detail)
	}
	// Non-secret content survives scrubbing.
	assert.Contains(t, repo.entries[0].Detail, "user=bob")
}

func TestQueryFilters(t *testing.T) {
	repo := &fakeRepo{}
	base := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	svc, err := New(repo, fixedClock{t: base})
	require.NoError(t, err)
	ctx := context.Background()

	_, err = svc.Append(ctx, Entry{ActorType: "user", ActorID: "u-1", Action: domain.AuditLogin, TargetType: "session", Outcome: OutcomeOK})
	require.NoError(t, err)
	svc2, _ := New(repo, fixedClock{t: base.Add(time.Hour)})
	_, err = svc2.Append(ctx, Entry{ActorType: "user", ActorID: "u-2", Action: domain.AuditLoginFailed, TargetType: "session", Outcome: OutcomeDenied})
	require.NoError(t, err)

	res, err := svc.Query(ctx, domain.ListOptions{Limit: 10}, Filter{})
	require.NoError(t, err)
	assert.Equal(t, 2, res.Total)

	res, err = svc.Query(ctx, domain.ListOptions{Limit: 10}, Filter{ActorID: "u-1"})
	require.NoError(t, err)
	require.Len(t, res.Items, 1)
	assert.Equal(t, "u-1", res.Items[0].ActorID)

	res, err = svc.Query(ctx, domain.ListOptions{Limit: 10}, Filter{Action: domain.AuditLoginFailed})
	require.NoError(t, err)
	require.Len(t, res.Items, 1)

	res, err = svc.Query(ctx, domain.ListOptions{Limit: 10}, Filter{Outcome: OutcomeDenied})
	require.NoError(t, err)
	require.Len(t, res.Items, 1)

	since := base.Add(30 * time.Minute)
	res, err = svc.Query(ctx, domain.ListOptions{Limit: 10}, Filter{Since: &since})
	require.NoError(t, err)
	require.Len(t, res.Items, 1)
	assert.Equal(t, "u-2", res.Items[0].ActorID)

	// Paging passes through to the repository.
	res, err = svc.Query(ctx, domain.ListOptions{Limit: 1, Offset: 1}, Filter{})
	require.NoError(t, err)
	assert.Equal(t, 2, res.Total)
	require.Len(t, res.Items, 1)
}
