package domain

import (
	"errors"
	"testing"
	"time"
)

func TestUserValidate(t *testing.T) {
	if err := (User{Username: "ada", Email: "ada@example.com"}).Validate(); err != nil {
		t.Fatalf("valid user rejected: %v", err)
	}
	for _, u := range []User{
		{Email: "a@b.c"},
		{Username: "ada"},
		{Username: "ada", Email: "not-an-email"},
	} {
		if err := u.Validate(); !errors.Is(err, ErrValidation) {
			t.Fatalf("user %+v: want ErrValidation, got %v", u, err)
		}
	}
}

func TestRoleForGroups(t *testing.T) {
	p := OIDCProvider{
		GroupRoleMapping: map[string]string{
			"vaultix-admins": RoleAdministrator,
			"vaultix-ops":    RoleOperator,
			"vaultix-view":   RoleReadOnly,
		},
		DefaultRole: RoleReadOnly,
	}
	role, ok := p.RoleForGroups([]string{"vaultix-view", "vaultix-ops"})
	if !ok || role != RoleOperator {
		t.Fatalf("want highest-privilege role operator, got %q ok=%v", role, ok)
	}
	role, ok = p.RoleForGroups([]string{"unknown-group"})
	if !ok || role != RoleReadOnly {
		t.Fatalf("want default role readonly, got %q ok=%v", role, ok)
	}
	p.DefaultRole = ""
	if _, ok = p.RoleForGroups([]string{"unknown-group"}); ok {
		t.Fatal("want denied access when no group maps and no default role")
	}
}

func TestOIDCProviderValidate(t *testing.T) {
	valid := OIDCProvider{Name: "corp", Issuer: "https://idp.example.com", ClientID: "vaultix"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid provider rejected: %v", err)
	}
	if got := valid.ScopesOrDefault(); len(got) != 3 || got[0] != "openid" {
		t.Fatalf("want default scopes, got %v", got)
	}
	for _, p := range []OIDCProvider{
		{Issuer: "https://x", ClientID: "c"},
		{Name: "n", ClientID: "c"},
		{Name: "n", Issuer: "https://x"},
	} {
		if err := p.Validate(); !errors.Is(err, ErrValidation) {
			t.Fatalf("provider %+v: want ErrValidation, got %v", p, err)
		}
	}
}

func TestAgentStatus(t *testing.T) {
	now := time.Now()
	recent := now.Add(-time.Minute)
	old := now.Add(-time.Hour)

	cases := []struct {
		name string
		a    Agent
		want AgentStatus
	}{
		{"revoked", Agent{Disabled: true, LastHeartbeatAt: &recent}, AgentStatusRevoked},
		{"never seen", Agent{}, AgentStatusOffline},
		{"online", Agent{LastHeartbeatAt: &recent}, AgentStatusOnline},
		{"stale", Agent{LastHeartbeatAt: &old}, AgentStatusStale},
	}
	for _, c := range cases {
		if got := c.a.Status(now, DefaultHeartbeatTimeout); got != c.want {
			t.Fatalf("%s: want %s, got %s", c.name, c.want, got)
		}
	}
}

func TestAgentCredentialValid(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	if !(AgentCredential{}).Valid(now) {
		t.Fatal("plain credential should be valid")
	}
	if (AgentCredential{Revoked: true}).Valid(now) {
		t.Fatal("revoked credential must be invalid")
	}
	if (AgentCredential{ExpiresAt: &past}).Valid(now) {
		t.Fatal("expired credential must be invalid")
	}
	if !(AgentCredential{ExpiresAt: &future}).Valid(now) {
		t.Fatal("unexpired credential should be valid")
	}
	c := AgentCredential{}
	c.Revoke()
	if c.Valid(now) {
		t.Fatal("Revoked credential must be invalid after Revoke()")
	}
}
