package storage

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vaultix/vaultix/internal/domain"
)

// fakeRepo is an in-memory domain.StorageRepository.
type fakeRepo struct {
	mu           sync.Mutex
	destinations map[string]*domain.StorageDestination
	secrets      map[string]*domain.SecretProfile
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		destinations: map[string]*domain.StorageDestination{},
		secrets:      map[string]*domain.SecretProfile{},
	}
}

func (f *fakeRepo) CreateDestination(_ context.Context, d *domain.StorageDestination) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.destinations[d.ID]; ok {
		return fmt.Errorf("%w: destination exists", domain.ErrConflict)
	}
	cp := *d
	f.destinations[d.ID] = &cp
	return nil
}

func (f *fakeRepo) GetDestination(_ context.Context, id string) (*domain.StorageDestination, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.destinations[id]
	if !ok {
		return nil, fmt.Errorf("%w: destination %q", domain.ErrNotFound, id)
	}
	cp := *d
	return &cp, nil
}

func (f *fakeRepo) GetDefaultDestination(_ context.Context) (*domain.StorageDestination, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, d := range f.destinations {
		if d.Default {
			cp := *d
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("%w: no default destination", domain.ErrNotFound)
}

func (f *fakeRepo) UpdateDestination(_ context.Context, d *domain.StorageDestination) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.destinations[d.ID]; !ok {
		return fmt.Errorf("%w: destination %q", domain.ErrNotFound, d.ID)
	}
	cp := *d
	f.destinations[d.ID] = &cp
	return nil
}

func (f *fakeRepo) DeleteDestination(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.destinations[id]; !ok {
		return fmt.Errorf("%w: destination %q", domain.ErrNotFound, id)
	}
	delete(f.destinations, id)
	return nil
}

func (f *fakeRepo) ListDestinations(_ context.Context) ([]domain.StorageDestination, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.StorageDestination, 0, len(f.destinations))
	for _, d := range f.destinations {
		out = append(out, *d)
	}
	return out, nil
}

func (f *fakeRepo) CreateSecret(_ context.Context, s *domain.SecretProfile) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.secrets[s.ID]; ok {
		return fmt.Errorf("%w: secret exists", domain.ErrConflict)
	}
	cp := *s
	f.secrets[s.ID] = &cp
	return nil
}

func (f *fakeRepo) GetSecret(_ context.Context, id string) (*domain.SecretProfile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.secrets[id]
	if !ok {
		return nil, fmt.Errorf("%w: secret %q", domain.ErrNotFound, id)
	}
	cp := *s
	return &cp, nil
}

func (f *fakeRepo) DeleteSecret(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.secrets[id]; !ok {
		return fmt.Errorf("%w: secret %q", domain.ErrNotFound, id)
	}
	delete(f.secrets, id)
	return nil
}

func (f *fakeRepo) ListSecrets(_ context.Context) ([]domain.SecretProfile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.SecretProfile, 0, len(f.secrets))
	for _, s := range f.secrets {
		out = append(out, *s)
	}
	return out, nil
}

// fakeRepoWithUpdate adds in-place secret updates.
type fakeRepoWithUpdate struct {
	*fakeRepo
}

func (f *fakeRepoWithUpdate) UpdateSecret(_ context.Context, s *domain.SecretProfile) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.secrets[s.ID]; !ok {
		return fmt.Errorf("%w: secret %q", domain.ErrNotFound, s.ID)
	}
	cp := *s
	f.secrets[s.ID] = &cp
	return nil
}

// fakeBox is a reversible test SecretBox. The encoding is a test-only
// stand-in, never a production cipher.
type fakeBox struct {
	mu     sync.Mutex
	seals  int
	opens  int
	keyRef string
}

func newFakeBox() *fakeBox {
	return &fakeBox{keyRef: "test-key-1"}
}

func (f *fakeBox) Seal(_ context.Context, plaintext []byte) ([]byte, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seals++
	out := append([]byte("sealed\x00"), plaintext...)
	return out, f.keyRef, nil
}

func (f *fakeBox) Open(_ context.Context, ciphertext []byte, _ string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opens++
	rest, ok := strings.CutPrefix(string(ciphertext), "sealed\x00")
	if !ok {
		return nil, fmt.Errorf("fakeBox: cannot open")
	}
	return []byte(rest), nil
}

// storedObject is the fake's integrity metadata for one key.
type storedObject struct {
	size     uint64
	checksum string
}

// fakeStore is an in-memory ObjectStore.
type fakeStore struct {
	mu       sync.Mutex
	objects  map[string]storedObject // "destID/key" -> meta
	uploads  map[string]string       // uploadID -> "destID/key"
	uploadN  int
	pingErr  error
	parts    map[string][]int // uploadID -> presigned parts
	aborted  map[string]bool
	finished map[string][]MultipartPart
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		objects:  map[string]storedObject{},
		uploads:  map[string]string{},
		parts:    map[string][]int{},
		aborted:  map[string]bool{},
		finished: map[string][]MultipartPart{},
	}
}

func (f *fakeStore) PresignUpload(_ context.Context, destinationID, objectKey string, _ time.Duration) (string, error) {
	return "https://signed.test/put?d=" + destinationID + "&k=" + objectKey, nil
}

func (f *fakeStore) PresignDownload(_ context.Context, destinationID, objectKey string, _ time.Duration) (string, error) {
	return "https://signed.test/get?d=" + destinationID + "&k=" + objectKey, nil
}

func (f *fakeStore) Head(_ context.Context, destinationID, objectKey string) (domain.ObjectMeta, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.objects[destinationID+"/"+objectKey]
	if !ok {
		return domain.ObjectMeta{Exists: false}, nil
	}
	return domain.ObjectMeta{Size: o.size, Checksum: o.checksum, Exists: true}, nil
}

func (f *fakeStore) Delete(_ context.Context, destinationID, objectKey string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.objects, destinationID+"/"+objectKey)
	return nil
}

func (f *fakeStore) Ping(_ context.Context, _ string) error {
	return f.pingErr
}

func (f *fakeStore) InitiateMultipartUpload(_ context.Context, destinationID, objectKey string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploadN++
	id := fmt.Sprintf("upload-%d", f.uploadN)
	f.uploads[id] = destinationID + "/" + objectKey
	return id, nil
}

func (f *fakeStore) PresignUploadPart(_ context.Context, destinationID, objectKey, uploadID string, partNumber int, ttl time.Duration) (PartGrant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.parts[uploadID] = append(f.parts[uploadID], partNumber)
	return PartGrant{
		PartNumber: partNumber,
		Method:     "PUT",
		URL:        "https://signed.test/part?u=" + uploadID + "&n=" + fmt.Sprint(partNumber) + "&k=" + objectKey + "&d=" + destinationID,
		ExpiresAt:  time.Now().Add(ttl),
	}, nil
}

func (f *fakeStore) CompleteMultipartUpload(_ context.Context, _, _ string, uploadID string, parts []MultipartPart) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finished[uploadID] = parts
	return nil
}

func (f *fakeStore) AbortMultipartUpload(_ context.Context, _, _ string, uploadID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.aborted[uploadID] = true
	return nil
}

func (f *fakeStore) VerifyObjects(_ context.Context, destinationID string, expected []ExpectedObject) (bool, []VerifyDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	allOK := true
	details := make([]VerifyDetail, 0, len(expected))
	for _, e := range expected {
		d := VerifyDetail{ObjectKey: e.ObjectKey, OK: true}
		o, ok := f.objects[destinationID+"/"+e.ObjectKey]
		switch {
		case !ok:
			d.OK, d.Reason = false, "object not found"
		case e.Checksum != "" && o.checksum != e.Checksum:
			d.OK, d.Reason = false, "checksum mismatch"
		case e.CheckSize && o.size != e.Size:
			d.OK, d.Reason = false, "size mismatch"
		}
		if !d.OK {
			allOK = false
		}
		details = append(details, d)
	}
	return allOK, details, nil
}

// fixedClock is a test domain.Clock.
type fixedClock struct {
	t time.Time
}

func (c fixedClock) Now() time.Time { return c.t }

// testService wires a Service with in-memory fakes.
func testService() (*Service, *fakeRepo, *fakeStore, *fakeBox) {
	repo := newFakeRepo()
	store := newFakeStore()
	box := newFakeBox()
	fixed := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	n := 0
	svc := NewService(repo, store, box,
		WithClock(fixedClock{t: fixed}),
		WithIDGenerator(func() string { n++; return fmt.Sprintf("id-%d", n) }),
	)
	return svc, repo, store, box
}

func mustDestination(t *testing.T, svc *Service, name string) *domain.StorageDestination {
	t.Helper()
	d, err := svc.CreateDestination(context.Background(), CreateDestinationInput{
		Name: name, Region: "us-east-1", Bucket: "test-bucket", Prefix: "tenant1",
	})
	require.NoError(t, err)
	return d
}
