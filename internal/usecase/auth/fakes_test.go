package auth

import (
	"context"
	"sync"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
)

// fakes for auth service tests. They implement the domain ports in memory.

type fakeUsers struct {
	mu      sync.Mutex
	users   map[string]*domain.User
	byName  map[string]string
	roles   map[string]*domain.Role
	byRole  map[string]string
	members map[string]map[string]string // userID -> roleID -> grantedBy
	seq     int
	// Fault injection for error-path tests.
	lookupErr error
	listErr   error
	getErr    error
	rolesErr  error
}

func newFakeUsers() *fakeUsers {
	f := &fakeUsers{
		users: map[string]*domain.User{}, byName: map[string]string{},
		roles: map[string]*domain.Role{}, byRole: map[string]string{},
		members: map[string]map[string]string{},
	}
	for _, name := range []string{domain.RoleAdministrator, domain.RoleOperator, domain.RoleReadOnly} {
		id := "role-" + name
		f.roles[id] = &domain.Role{ID: id, Name: name}
		f.byRole[name] = id
	}
	return f
}

func (f *fakeUsers) CreateUser(_ context.Context, u *domain.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, dup := f.byName[u.Username]; dup {
		return domain.ErrConflict
	}
	f.seq++
	u.ID = "user-" + itoa(f.seq)
	u.CreatedAt, u.UpdatedAt = time.Now(), time.Now()
	cp := *u
	f.users[u.ID] = &cp
	f.byName[u.Username] = u.ID
	return nil
}

func (f *fakeUsers) GetUser(_ context.Context, id string) (*domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, f.getErr
	}
	u, ok := f.users[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *u
	return &cp, nil
}

func (f *fakeUsers) GetUserByUsername(_ context.Context, username string) (*domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lookupErr != nil {
		return nil, f.lookupErr
	}
	id, ok := f.byName[username]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *f.users[id]
	return &cp, nil
}

func (f *fakeUsers) UpdateUser(_ context.Context, u *domain.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.users[u.ID]; !ok {
		return domain.ErrNotFound
	}
	cp := *u
	f.users[u.ID] = &cp
	f.byName[u.Username] = u.ID
	return nil
}

func (f *fakeUsers) ListUsers(_ context.Context, opts domain.ListOptions) (domain.ListResult[domain.User], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return domain.ListResult[domain.User]{}, f.listErr
	}
	var out []domain.User
	for _, u := range f.users {
		out = append(out, *u)
	}
	return domain.ListResult[domain.User]{Items: out, Total: len(out)}, nil
}

func (f *fakeUsers) GetRole(_ context.Context, id string) (*domain.Role, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.roles[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *r
	return &cp, nil
}

func (f *fakeUsers) GetRoleByName(_ context.Context, name string) (*domain.Role, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.byRole[name]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *f.roles[id]
	return &cp, nil
}

func (f *fakeUsers) ListRoles(_ context.Context) ([]domain.Role, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Role
	for _, r := range f.roles {
		out = append(out, *r)
	}
	return out, nil
}

func (f *fakeUsers) ListUserRoles(_ context.Context, userID string) ([]domain.Role, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rolesErr != nil {
		return nil, f.rolesErr
	}
	var out []domain.Role
	for roleID := range f.members[userID] {
		if r, ok := f.roles[roleID]; ok {
			out = append(out, *r)
		}
	}
	return out, nil
}

func (f *fakeUsers) GrantRole(_ context.Context, userID, roleID, grantedBy string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.users[userID]; !ok {
		return domain.ErrNotFound
	}
	if _, ok := f.roles[roleID]; !ok {
		return domain.ErrNotFound
	}
	if f.members[userID] == nil {
		f.members[userID] = map[string]string{}
	}
	f.members[userID][roleID] = grantedBy
	return nil
}

func (f *fakeUsers) RevokeRole(_ context.Context, userID, roleID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.members[userID], roleID)
	return nil
}

func (f *fakeUsers) ReplaceUserRoles(_ context.Context, userID string, roleIDs []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.members[userID] = map[string]string{}
	for _, id := range roleIDs {
		f.members[userID][id] = ""
	}
	return nil
}

type fakeProviders struct {
	mu     sync.Mutex
	provs  map[string]*domain.OIDCProvider
	idents map[string]*domain.UserExternalIdentity // by id
	bySub  map[string]string                       // issuer+"\x00"+subject -> id
	seq    int
	// Fault injection for error-path tests.
	listErr error
}

func newFakeProviders() *fakeProviders {
	return &fakeProviders{
		provs:  map[string]*domain.OIDCProvider{},
		idents: map[string]*domain.UserExternalIdentity{},
		bySub:  map[string]string{},
	}
}

func (f *fakeProviders) CreateProvider(_ context.Context, p *domain.OIDCProvider) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	p.ID = "prov-" + itoa(f.seq)
	cp := *p
	f.provs[p.ID] = &cp
	return nil
}

func (f *fakeProviders) GetProvider(_ context.Context, id string) (*domain.OIDCProvider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.provs[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *p
	return &cp, nil
}

func (f *fakeProviders) GetProviderByIssuer(_ context.Context, issuer string) (*domain.OIDCProvider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.provs {
		if p.Issuer == issuer {
			cp := *p
			return &cp, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (f *fakeProviders) UpdateProvider(_ context.Context, p *domain.OIDCProvider) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.provs[p.ID]; !ok {
		return domain.ErrNotFound
	}
	cp := *p
	f.provs[p.ID] = &cp
	return nil
}

func (f *fakeProviders) ListProviders(_ context.Context) ([]domain.OIDCProvider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []domain.OIDCProvider
	for _, p := range f.provs {
		out = append(out, *p)
	}
	return out, nil
}

func identKey(issuer, subject string) string { return issuer + "\x00" + subject }

func (f *fakeProviders) LinkIdentity(_ context.Context, i *domain.UserExternalIdentity) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, dup := f.bySub[identKey(i.Issuer, i.Subject)]; dup {
		return domain.ErrConflict
	}
	f.seq++
	i.ID = "ident-" + itoa(f.seq)
	cp := *i
	f.idents[i.ID] = &cp
	f.bySub[identKey(i.Issuer, i.Subject)] = i.ID
	return nil
}

func (f *fakeProviders) GetIdentity(_ context.Context, issuer, subject string) (*domain.UserExternalIdentity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.bySub[identKey(issuer, subject)]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *f.idents[id]
	return &cp, nil
}

func (f *fakeProviders) ListUserIdentities(_ context.Context, userID string) ([]domain.UserExternalIdentity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.UserExternalIdentity
	for _, i := range f.idents {
		if i.UserID == userID {
			out = append(out, *i)
		}
	}
	return out, nil
}

func (f *fakeProviders) UpdateIdentity(_ context.Context, i *domain.UserExternalIdentity) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.idents[i.ID]; !ok {
		return domain.ErrNotFound
	}
	cp := *i
	f.idents[i.ID] = &cp
	return nil
}

func (f *fakeProviders) UnlinkIdentity(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	i, ok := f.idents[id]
	if !ok {
		return domain.ErrNotFound
	}
	delete(f.bySub, identKey(i.Issuer, i.Subject))
	delete(f.idents, id)
	return nil
}

type fakeSessions struct {
	mu   sync.Mutex
	byID map[string]*domain.Session
	byH  map[string]string
	seq  int
	// touches counts Touch calls (lazy-touch assertions).
	touches int
	// Fault injection for error-path tests.
	getErr    error
	touchErr  error
	createErr error
}

func newFakeSessions() *fakeSessions {
	return &fakeSessions{byID: map[string]*domain.Session{}, byH: map[string]string{}}
}

func (f *fakeSessions) Create(_ context.Context, s *domain.Session) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return f.createErr
	}
	f.seq++
	s.ID = "sess-" + itoa(f.seq)
	cp := *s
	f.byID[s.ID] = &cp
	f.byH[s.TokenHash] = s.ID
	return nil
}

func (f *fakeSessions) GetByTokenHash(_ context.Context, h string) (*domain.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, f.getErr
	}
	id, ok := f.byH[h]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *f.byID[id]
	return &cp, nil
}

func (f *fakeSessions) Touch(_ context.Context, id string, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.touchErr != nil {
		return f.touchErr
	}
	s, ok := f.byID[id]
	if !ok {
		return domain.ErrNotFound
	}
	f.touches++
	s.LastSeenAt = at
	return nil
}

func (f *fakeSessions) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.byID[id]; ok {
		delete(f.byH, s.TokenHash)
		delete(f.byID, id)
	}
	return nil
}

func (f *fakeSessions) DeleteByTokenHash(_ context.Context, h string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, ok := f.byH[h]; ok {
		delete(f.byH, h)
		delete(f.byID, id)
	}
	return nil
}

func (f *fakeSessions) DeleteUserSessions(_ context.Context, userID, exceptID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, s := range f.byID {
		if s.UserID == userID && id != exceptID {
			delete(f.byH, s.TokenHash)
			delete(f.byID, id)
		}
	}
	return nil
}

func (f *fakeSessions) DeleteExpired(_ context.Context, now time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for id, s := range f.byID {
		if s.Expired(now) {
			delete(f.byH, s.TokenHash)
			delete(f.byID, id)
			n++
		}
	}
	return n, nil
}

type fakeOIDC struct {
	mu          sync.Mutex
	claims      *domain.OIDCClaims
	exchErr     error
	authURL     string
	authErr     error
	logoutURL   string
	discoverErr error
	lastNonce   string
	lastState   string
}

func (f *fakeOIDC) AuthURL(_ *domain.OIDCProvider, state, nonce, challenge, redirectURI string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastState, f.lastNonce = state, nonce
	if f.authErr != nil {
		return "", f.authErr
	}
	if f.authURL != "" {
		return f.authURL, nil
	}
	return "https://idp.example/auth?state=" + state + "&nonce=" + nonce + "&cc=" + challenge + "&ru=" + redirectURI, nil
}

func (f *fakeOIDC) Exchange(_ context.Context, _ *domain.OIDCProvider, _, _, _ /*code, verifier, redirect*/, expectedNonce string) (*domain.OIDCClaims, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.exchErr != nil {
		return nil, f.exchErr
	}
	if f.claims == nil {
		return nil, ErrInvalidState
	}
	cp := *f.claims
	_ = expectedNonce
	return &cp, nil
}

func (f *fakeOIDC) LogoutURL(_ *domain.OIDCProvider, postLogoutRedirect string) string {
	if f.logoutURL != "" {
		return f.logoutURL + "?post=" + postLogoutRedirect
	}
	return ""
}

func (f *fakeOIDC) DiscoverProvider(_ context.Context, _ *domain.OIDCProvider) error {
	return f.discoverErr
}

func (f *fakeOIDC) TestProvider(_ context.Context, p *domain.OIDCProvider) (*ProviderTestReport, error) {
	return &ProviderTestReport{OK: true, Issuer: p.Issuer, KeyCount: 1, HasUserInfo: true}, nil
}

type fakeSecrets struct {
	mu sync.Mutex
	m  map[string]string
}

func newFakeSecrets() *fakeSecrets { return &fakeSecrets{m: map[string]string{}} }

func (f *fakeSecrets) GetProviderSecret(_ context.Context, id string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.m[id]
	if !ok {
		return "", domain.ErrNotFound
	}
	return v, nil
}

func (f *fakeSecrets) SetProviderSecret(_ context.Context, id, ct string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[id] = ct
	return nil
}

func (f *fakeSecrets) DeleteProviderSecret(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.m, id)
	return nil
}

// fakeBox is a reversible (non-secure) SecretBox for service tests; real
// encryption is covered in adapter/crypto.
type fakeBox struct{}

func (fakeBox) Encrypt(pt []byte) (string, error) { return "enc:" + string(pt), nil }
func (fakeBox) Decrypt(env string) ([]byte, error) {
	if len(env) < 4 || env[:4] != "enc:" {
		return nil, errFakeBox
	}
	return []byte(env[4:]), nil
}

type fakeBoxErr string

func (e fakeBoxErr) Error() string { return string(e) }

const errFakeBox = fakeBoxErr("fakebox: decrypt failed")

type fakeAudit struct {
	mu     sync.Mutex
	events []AuditEvent
}

func (f *fakeAudit) RecordAuthEvent(_ context.Context, e AuditEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
	return nil
}

func (f *fakeAudit) actions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, e := range f.events {
		out = append(out, e.Action+":"+e.Result)
	}
	return out
}

func (f *fakeAudit) has(action, result string) bool {
	for _, a := range f.actions() {
		if a == action+":"+result {
			return true
		}
	}
	return false
}

type fixedClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fixedClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fixedClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
