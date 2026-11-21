package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testDirective(t *testing.T, payload []byte) *UpdateDirective {
	t.Helper()
	sum := sha256.Sum256(payload)
	return &UpdateDirective{
		Version: "v2.0.0", GOOS: "linux", GOARCH: "amd64",
		SHA256: hex.EncodeToString(sum[:]), SizeBytes: int64(len(payload)),
	}
}

func writeFakeExe(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o755))
	return path
}

func TestApplySkips(t *testing.T) {
	stub := &StubManagerClient{}
	stub.OnDownloadUpdate = func() (UpdatePackage, error) {
		t.Fatal("must not download")
		return UpdatePackage{}, nil
	}
	u := &Updater{Client: stub, Version: "v2.0.0"}
	ctx := context.Background()
	require.NoError(t, u.Apply(ctx, nil, func() bool { return true }))
	require.Error(t, u.Apply(ctx, &UpdateDirective{Version: ""}, func() bool { return true }))
	// Stale target: quiet no-op.
	require.NoError(t, u.Apply(ctx, &UpdateDirective{Version: "v1.0.0"}, func() bool { return true }))
	// Busy agent defers.
	dir := testDirective(t, []byte("new-bytes"))
	require.NoError(t, u.Apply(ctx, dir, func() bool { return false }))
	assert.Empty(t, stub.UpdateReports)
}

func TestApplySwapsAndExecs(t *testing.T) {
	dir := t.TempDir()
	exe := writeFakeExe(t, dir, "vaultix-agent", "old-binary")
	payload := []byte("new-binary-bytes")
	stub := &StubManagerClient{}
	stub.OnDownloadUpdate = func() (UpdatePackage, error) {
		return UpdatePackage{Version: "v2.0.0", Bytes: payload}, nil
	}
	var execed string
	u := &Updater{
		Client: stub, Version: "v1.0.0", ExecPath: exe,
		RunVersion: func(_ context.Context, bin string) error {
			data, err := os.ReadFile(bin)
			if err != nil {
				return err
			}
			if string(data) != string(payload) {
				return errors.New("staged content mismatch")
			}
			return nil
		},
		Exec: func(argv0 string, _, _ []string) error {
			execed = argv0
			return nil
		},
	}
	require.NoError(t, u.Apply(context.Background(), testDirective(t, payload), func() bool { return true }))
	assert.Equal(t, exe, execed)
	data, err := os.ReadFile(exe)
	require.NoError(t, err)
	assert.Equal(t, string(payload), string(data))
	backup, err := os.ReadFile(exe + ".prev")
	require.NoError(t, err)
	assert.Equal(t, "old-binary", string(backup))
	require.Len(t, stub.UpdateReports, 1)
	assert.Equal(t, "applied", stub.UpdateReports[0].Status)
	assert.Equal(t, "v2.0.0", stub.UpdateReports[0].Version)
}

func TestApplyRejectsBadBytes(t *testing.T) {
	dir := t.TempDir()
	exe := writeFakeExe(t, dir, "vaultix-agent", "old-binary")
	stub := &StubManagerClient{}
	stub.OnDownloadUpdate = func() (UpdatePackage, error) {
		return UpdatePackage{Version: "v2.0.0", Bytes: []byte("tampered")}, nil
	}
	u := &Updater{
		Client: stub, Version: "v1.0.0", ExecPath: exe,
		Exec: func(_ string, _, _ []string) error {
			t.Fatal("must not exec on mismatch")
			return nil
		},
	}
	err := u.Apply(context.Background(), testDirective(t, []byte("genuine")), func() bool { return true })
	require.Error(t, err)
	data, _ := os.ReadFile(exe)
	assert.Equal(t, "old-binary", string(data), "exe must be untouched")
	require.Len(t, stub.UpdateReports, 1)
	assert.Equal(t, "failed", stub.UpdateReports[0].Status)
}

func TestApplySmokeFailureCleansUp(t *testing.T) {
	dir := t.TempDir()
	exe := writeFakeExe(t, dir, "vaultix-agent", "old-binary")
	payload := []byte("new-binary-bytes")
	stub := &StubManagerClient{}
	stub.OnDownloadUpdate = func() (UpdatePackage, error) {
		return UpdatePackage{Version: "v2.0.0", Bytes: payload}, nil
	}
	u := &Updater{
		Client: stub, Version: "v1.0.0", ExecPath: exe,
		RunVersion: func(_ context.Context, _ string) error { return errors.New("exit 1") },
	}
	err := u.Apply(context.Background(), testDirective(t, payload), func() bool { return true })
	require.Error(t, err)
	_, statErr := os.Stat(exe + ".new")
	assert.True(t, os.IsNotExist(statErr), "staged file must be removed")
	data, _ := os.ReadFile(exe)
	assert.Equal(t, "old-binary", string(data))
	require.Len(t, stub.UpdateReports, 1)
	assert.Equal(t, "failed", stub.UpdateReports[0].Status)
}

func TestApplyExecFailureRestores(t *testing.T) {
	dir := t.TempDir()
	exe := writeFakeExe(t, dir, "vaultix-agent", "old-binary")
	payload := []byte("new-binary-bytes")
	stub := &StubManagerClient{}
	stub.OnDownloadUpdate = func() (UpdatePackage, error) {
		return UpdatePackage{Version: "v2.0.0", Bytes: payload}, nil
	}
	u := &Updater{
		Client: stub, Version: "v1.0.0", ExecPath: exe,
		RunVersion: func(_ context.Context, _ string) error { return nil },
		Exec:       func(_ string, _, _ []string) error { return errors.New("exec denied") },
	}
	err := u.Apply(context.Background(), testDirective(t, payload), func() bool { return true })
	require.Error(t, err)
	data, _ := os.ReadFile(exe)
	assert.Equal(t, "old-binary", string(data), "backup must be restored")
	statuses := []string{stub.UpdateReports[0].Status, stub.UpdateReports[1].Status}
	assert.Equal(t, []string{"applied", "failed"}, statuses)
}
