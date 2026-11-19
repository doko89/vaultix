package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
	ustorage "github.com/vaultix/vaultix/internal/usecase/storage"
)

// errBoom is a marker failure whose text must never cross the API boundary.
var errBoom = errors.New("boom-marker: connection refused")

// memRepo is an in-memory domain.StorageRepository.
type memRepo struct {
	mu           sync.Mutex
	destinations map[string]*domain.StorageDestination
	secrets      map[string]*domain.SecretProfile
	seq          int
}

func (m *memRepo) nextID() string {
	m.seq++
	return fmt.Sprintf("http-%d", m.seq)
}

func (m *memRepo) CreateDestination(_ context.Context, d *domain.StorageDestination) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d.ID == "" {
		d.ID = m.nextID()
	}
	cp := *d
	m.destinations[d.ID] = &cp
	return nil
}

func (m *memRepo) GetDestination(_ context.Context, id string) (*domain.StorageDestination, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.destinations[id]
	if !ok {
		return nil, fmt.Errorf("%w: destination %q", domain.ErrNotFound, id)
	}
	cp := *d
	return &cp, nil
}

func (m *memRepo) GetDefaultDestination(_ context.Context) (*domain.StorageDestination, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.destinations {
		if d.Default {
			cp := *d
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("%w: no default destination", domain.ErrNotFound)
}

func (m *memRepo) UpdateDestination(_ context.Context, d *domain.StorageDestination) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.destinations[d.ID]; !ok {
		return fmt.Errorf("%w: destination %q", domain.ErrNotFound, d.ID)
	}
	cp := *d
	m.destinations[d.ID] = &cp
	return nil
}

func (m *memRepo) DeleteDestination(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.destinations[id]; !ok {
		return fmt.Errorf("%w: destination %q", domain.ErrNotFound, id)
	}
	delete(m.destinations, id)
	return nil
}

func (m *memRepo) ListDestinations(_ context.Context) ([]domain.StorageDestination, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]domain.StorageDestination, 0, len(m.destinations))
	for _, d := range m.destinations {
		out = append(out, *d)
	}
	return out, nil
}

func (m *memRepo) CreateSecret(_ context.Context, s *domain.SecretProfile) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.ID == "" {
		s.ID = m.nextID()
	}
	cp := *s
	m.secrets[s.ID] = &cp
	return nil
}

func (m *memRepo) GetSecret(_ context.Context, id string) (*domain.SecretProfile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.secrets[id]
	if !ok {
		return nil, fmt.Errorf("%w: secret %q", domain.ErrNotFound, id)
	}
	cp := *s
	return &cp, nil
}

func (m *memRepo) DeleteSecret(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.secrets[id]; !ok {
		return fmt.Errorf("%w: secret %q", domain.ErrNotFound, id)
	}
	delete(m.secrets, id)
	return nil
}

func (m *memRepo) ListSecrets(_ context.Context) ([]domain.SecretProfile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]domain.SecretProfile, 0, len(m.secrets))
	for _, s := range m.secrets {
		out = append(out, *s)
	}
	return out, nil
}

// memObject is stored integrity metadata for one key.
type memObject struct {
	size     uint64
	checksum string
}

// memStore is an in-memory ustorage.ObjectStore.
type memStore struct {
	mu      sync.Mutex
	objects map[string]memObject
	pingErr error
}

func (m *memStore) PresignUpload(_ context.Context, _, _ string, _ time.Duration) (string, error) {
	return "https://signed.test/put", nil
}

func (m *memStore) PresignDownload(_ context.Context, _, _ string, _ time.Duration) (string, error) {
	return "https://signed.test/get", nil
}

func (m *memStore) Head(_ context.Context, destinationID, objectKey string) (domain.ObjectMeta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.objects[destinationID+"/"+objectKey]
	if !ok {
		return domain.ObjectMeta{Exists: false}, nil
	}
	return domain.ObjectMeta{Size: o.size, Checksum: o.checksum, Exists: true}, nil
}

func (m *memStore) Delete(_ context.Context, destinationID, objectKey string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, destinationID+"/"+objectKey)
	return nil
}

func (m *memStore) Ping(_ context.Context, _ string) error {
	return m.pingErr
}

func (m *memStore) InitiateMultipartUpload(_ context.Context, _, _ string) (string, error) {
	return "upload-1", nil
}

func (m *memStore) PresignUploadPart(_ context.Context, _, _, _ string, partNumber int, ttl time.Duration) (ustorage.PartGrant, error) {
	return ustorage.PartGrant{PartNumber: partNumber, Method: "PUT", URL: "https://signed.test/part", ExpiresAt: time.Now().Add(ttl)}, nil
}

func (m *memStore) CompleteMultipartUpload(_ context.Context, _, _, _ string, _ []ustorage.MultipartPart) error {
	return nil
}

func (m *memStore) AbortMultipartUpload(_ context.Context, _, _, _ string) error {
	return nil
}

func (m *memStore) VerifyObjects(_ context.Context, _ string, _ []ustorage.ExpectedObject) (bool, []ustorage.VerifyDetail, error) {
	return true, nil, nil
}

// memBox is a reversible test ustorage.SecretBox (test-only stand-in).
type memBox struct {
	mu sync.Mutex
	n  int
}

func (m *memBox) Seal(_ context.Context, plaintext []byte) ([]byte, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.n++
	out := append([]byte("sealed\x00"), plaintext...)
	return out, "mem-key-1", nil
}

func (m *memBox) Open(_ context.Context, ciphertext []byte, _ string) ([]byte, error) {
	rest, ok := strings.CutPrefix(string(ciphertext), "sealed\x00")
	if !ok {
		return nil, errors.New("memBox: cannot open")
	}
	return []byte(rest), nil
}
