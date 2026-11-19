package agent

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeTarZst builds a tar+zstd payload from name->content entries.
func makeTarZst(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, TarZstd(&buf, 1, func(tw *tar.Writer) error {
		for name, content := range entries {
			if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
				return err
			}
			if _, err := tw.Write([]byte(content)); err != nil {
				return err
			}
		}
		return nil
	}))
	return buf.Bytes()
}

// decodeZst exposes the tar stream inside a tar+zstd payload, mirroring the
// restore pipeline (decrypt → zstd-decode → extract).
func decodeZst(t *testing.T, raw []byte) *zstd.Decoder {
	t.Helper()
	dec, err := zstd.NewReader(bytes.NewReader(raw))
	require.NoError(t, err)
	t.Cleanup(dec.Close)
	return dec
}

func TestExtractTarRoundTrip(t *testing.T) {
	raw := makeTarZst(t, map[string]string{"a.txt": "hello", "sub/b.txt": "world"})
	dest := t.TempDir()
	out := filepath.Join(dest, "out")
	require.NoError(t, ExtractTar(decodeZst(t, raw), out, false, 1<<20))
	got, err := os.ReadFile(filepath.Join(out, "a.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello", string(got))
	got, err = os.ReadFile(filepath.Join(out, "sub", "b.txt"))
	require.NoError(t, err)
	assert.Equal(t, "world", string(got))
}

func TestExtractTarRefusesOverwrite(t *testing.T) {
	raw := makeTarZst(t, map[string]string{"a.txt": "new"})
	out := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(out, "a.txt"), []byte("old"), 0o644))
	err := ExtractTar(decodeZst(t, raw), out, false, 1<<20)
	require.ErrorContains(t, err, "refusing to overwrite")
	got, err := os.ReadFile(filepath.Join(out, "a.txt"))
	require.NoError(t, err)
	assert.Equal(t, "old", string(got), "refused overwrite must leave the file untouched")

	require.NoError(t, ExtractTar(decodeZst(t, raw), out, true, 1<<20))
	got, err = os.ReadFile(filepath.Join(out, "a.txt"))
	require.NoError(t, err)
	assert.Equal(t, "new", string(got))
}

func TestExtractTarRejectsTraversal(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, TarZstd(&buf, 1, func(tw *tar.Writer) error {
		if err := tw.WriteHeader(&tar.Header{Name: "../evil", Mode: 0o644, Size: 3, Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err := tw.Write([]byte("bad"))
		return err
	}))
	err := ExtractTar(decodeZst(t, buf.Bytes()), t.TempDir(), true, 1<<20)
	require.ErrorContains(t, err, "escapes destination")
}

func TestExtractTarRejectsAbsoluteSymlink(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, TarZstd(&buf, 1, func(tw *tar.Writer) error {
		return tw.WriteHeader(&tar.Header{Name: "evil", Mode: 0o777, Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"})
	}))
	err := ExtractTar(decodeZst(t, buf.Bytes()), t.TempDir(), true, 1<<20)
	require.ErrorContains(t, err, "absolute path")
}

func TestExtractTarRestoresSafeSymlink(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, TarZstd(&buf, 1, func(tw *tar.Writer) error {
		if err := tw.WriteHeader(&tar.Header{Name: "real.txt", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		if _, err := tw.Write([]byte("r")); err != nil {
			return err
		}
		return tw.WriteHeader(&tar.Header{Name: "link.txt", Mode: 0o777, Typeflag: tar.TypeSymlink, Linkname: "real.txt"})
	}))
	out := t.TempDir()
	require.NoError(t, ExtractTar(decodeZst(t, buf.Bytes()), out, false, 1<<20))
	target, err := os.Readlink(filepath.Join(out, "link.txt"))
	require.NoError(t, err)
	assert.Equal(t, "real.txt", target)
}

func TestRestoreObjectShaMismatch(t *testing.T) {
	payload := makeTarZst(t, map[string]string{"a.txt": "data"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	r := &Restorer{Manager: &StubManagerClient{}, WorkDir: t.TempDir()}
	job := RestoreJob{ID: "r-sha", Kind: "filesystem",
		Destination:     RestoreDestination{Mode: "path", Path: t.TempDir()},
		OverwritePolicy: "allow",
		Objects:         []RestoreObject{{Key: "k", SizeBytes: int64(len(payload)), SHA256: "deadbeef"}},
	}
	err := r.restoreObject(context.Background(), job, job.Objects[0], DownloadGrant{Key: "k", URL: srv.URL + "/o"})
	require.ErrorContains(t, err, "sha256 mismatch")
}

func TestRestoreExecuteEndToEnd(t *testing.T) {
	payload := makeTarZst(t, map[string]string{"db/a.txt": "restored!"})
	sum := sha256.Sum256(payload)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	stub := &StubManagerClient{}
	stub.OnDownloadGrants = func(rid string, keys []string) (DownloadGrantsResponse, error) {
		return DownloadGrantsResponse{Grants: []DownloadGrant{{Key: keys[0], URL: srv.URL + "/o"}}}, nil
	}
	var reported RestoreReportRequest
	stub.OnReportRestore = func(rid string, req RestoreReportRequest) error {
		reported = req
		return nil
	}
	r := &Restorer{Manager: stub, WorkDir: t.TempDir()}
	dest := t.TempDir()
	state, err := r.Execute(context.Background(), RestoreJob{
		ID: "r-1", SnapshotID: "s-1", Kind: "filesystem",
		Destination:     RestoreDestination{Mode: "path", Path: dest},
		OverwritePolicy: "allow",
		Objects:         []RestoreObject{{Key: "p/snap/r-1/t0", SizeBytes: int64(len(payload)), SHA256: hex.EncodeToString(sum[:])}},
	})
	require.NoError(t, err)
	assert.Equal(t, "succeeded", state)
	assert.Equal(t, "succeeded", reported.State)
	got, err := os.ReadFile(filepath.Join(dest, "db", "a.txt"))
	require.NoError(t, err)
	assert.Equal(t, "restored!", string(got))
}

func TestExtractDirModes(t *testing.T) {
	orig := RestoreJob{Destination: RestoreDestination{Mode: "original", Path: "/srv/data"}}
	assert.Equal(t, "/srv", extractDir(orig))
	alt := RestoreJob{Destination: RestoreDestination{Mode: "alternate", Path: "/tmp/restore"}}
	assert.Equal(t, "/tmp/restore", extractDir(alt))
	legacy := RestoreJob{Destination: RestoreDestination{Mode: "path", Path: "/tmp/legacy"}}
	assert.Equal(t, "/tmp/legacy", extractDir(legacy))
}

func TestDestDirAnchorsRelativeAtWorkDir(t *testing.T) {
	r := &Restorer{WorkDir: "/tmp/wd"}
	assert.Equal(t, "/tmp/wd/testapp_restored", r.destDir("testapp_restored"))
	assert.Equal(t, "/abs/dir", r.destDir("/abs/dir"))
	assert.Equal(t, filepath.Join(os.TempDir(), "db"), (&Restorer{}).destDir("db"))
}

func TestRestoreExecuteOriginalModeExtractsIntoParent(t *testing.T) {
	// Archives store entries under the source base name; an in-place
	// restore of /tmp/x/data must land back at /tmp/x/data, not nest.
	root := t.TempDir()
	payload := makeTarZst(t, map[string]string{"data/a.txt": "in-place!"})
	sum := sha256.Sum256(payload)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	stub := &StubManagerClient{}
	stub.OnDownloadGrants = func(rid string, keys []string) (DownloadGrantsResponse, error) {
		return DownloadGrantsResponse{Grants: []DownloadGrant{{Key: keys[0], URL: srv.URL + "/o"}}}, nil
	}
	stub.OnReportRestore = func(rid string, req RestoreReportRequest) error { return nil }
	r := &Restorer{Manager: stub, WorkDir: t.TempDir()}
	state, err := r.Execute(context.Background(), RestoreJob{
		ID: "r-1", SnapshotID: "s-1", Kind: "filesystem",
		Destination:     RestoreDestination{Mode: "original", Path: filepath.Join(root, "data")},
		OverwritePolicy: "allow",
		Objects:         []RestoreObject{{Key: "p/snap/r-1/t0", SizeBytes: int64(len(payload)), SHA256: hex.EncodeToString(sum[:])}},
	})
	require.NoError(t, err)
	assert.Equal(t, "succeeded", state)
	got, err := os.ReadFile(filepath.Join(root, "data", "a.txt"))
	require.NoError(t, err)
	assert.Equal(t, "in-place!", string(got))
}

func TestRestoreExecuteEncrypted(t *testing.T) {
	payload := makeTarZst(t, map[string]string{"f.txt": "plain"})
	key := testKeyB64(t)
	var env bytes.Buffer
	_, _, err := EncryptStream(&env, bytes.NewReader(payload), key, "k1")
	require.NoError(t, err)
	sum := sha256.Sum256(env.Bytes())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(env.Bytes())
	}))
	defer srv.Close()

	stub := &StubManagerClient{}
	stub.OnDownloadGrants = func(rid string, keys []string) (DownloadGrantsResponse, error) {
		return DownloadGrantsResponse{Grants: []DownloadGrant{{Key: keys[0], URL: srv.URL + "/o"}}}, nil
	}
	r := &Restorer{Manager: stub, WorkDir: t.TempDir()}
	dest := t.TempDir()
	state, err := r.Execute(context.Background(), RestoreJob{
		ID: "r-enc", SnapshotID: "s-1", Kind: "filesystem",
		Destination:     RestoreDestination{Mode: "path", Path: dest},
		OverwritePolicy: "allow",
		Objects:         []RestoreObject{{Key: "k", SizeBytes: int64(env.Len()), SHA256: hex.EncodeToString(sum[:])}},
		Encryption:      RestoreEncryption{Enabled: true, KeyRef: "k1", KeyB64: key},
	})
	require.NoError(t, err)
	assert.Equal(t, "succeeded", state)
	got, err := os.ReadFile(filepath.Join(dest, "f.txt"))
	require.NoError(t, err)
	assert.Equal(t, "plain", string(got))
}

func TestRestoreExecuteReportsFailure(t *testing.T) {
	stub := &StubManagerClient{}
	stub.OnDownloadGrants = func(rid string, keys []string) (DownloadGrantsResponse, error) {
		return DownloadGrantsResponse{}, fmt.Errorf("boom")
	}
	var reported RestoreReportRequest
	stub.OnReportRestore = func(rid string, req RestoreReportRequest) error {
		reported = req
		return nil
	}
	r := &Restorer{Manager: stub, WorkDir: t.TempDir()}
	state, err := r.Execute(context.Background(), RestoreJob{
		ID: "r-fail", Kind: "filesystem",
		Destination:     RestoreDestination{Mode: "path", Path: t.TempDir()},
		OverwritePolicy: "allow",
		Objects:         []RestoreObject{{Key: "k"}},
	})
	require.Error(t, err)
	assert.Equal(t, "failed", state)
	assert.Equal(t, "failed", reported.State)
	assert.Contains(t, reported.Error, "download grants")
}

func TestRestoreExecuteRefusesOverwriteWithoutAllow(t *testing.T) {
	payload := makeTarZst(t, map[string]string{"a.txt": "new"})
	sum := sha256.Sum256(payload)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()
	stub := &StubManagerClient{}
	stub.OnDownloadGrants = func(rid string, keys []string) (DownloadGrantsResponse, error) {
		return DownloadGrantsResponse{Grants: []DownloadGrant{{Key: keys[0], URL: srv.URL + "/o"}}}, nil
	}
	var reported RestoreReportRequest
	stub.OnReportRestore = func(rid string, req RestoreReportRequest) error {
		reported = req
		return nil
	}
	r := &Restorer{Manager: stub, WorkDir: t.TempDir()}
	dest := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dest, "a.txt"), []byte("precious"), 0o644))
	state, err := r.Execute(context.Background(), RestoreJob{
		ID: "r-refuse", Kind: "filesystem",
		Destination:     RestoreDestination{Mode: "path", Path: dest},
		OverwritePolicy: "refuse",
		Objects:         []RestoreObject{{Key: "k", SizeBytes: int64(len(payload)), SHA256: hex.EncodeToString(sum[:])}},
	})
	require.ErrorContains(t, err, "refusing to overwrite")
	assert.Equal(t, "failed", state)
	assert.Contains(t, reported.Error, "refusing to overwrite")
}

func TestRestoreExecuteRejectsBadRequests(t *testing.T) {
	stub := &StubManagerClient{}
	r := &Restorer{Manager: stub, WorkDir: t.TempDir()}
	cases := []struct {
		name    string
		job     RestoreJob
		wantErr string
	}{
		{"missing id", RestoreJob{}, "missing id"},
		{"missing dest", RestoreJob{ID: "r", Objects: []RestoreObject{{Key: "k"}}}, "missing destination"},
		{"no objects", RestoreJob{ID: "r", Destination: RestoreDestination{Path: t.TempDir()}}, "no objects"},
		{"encrypted no key", RestoreJob{
			ID: "r", Destination: RestoreDestination{Path: t.TempDir()},
			Objects: []RestoreObject{{Key: "k"}}, Encryption: RestoreEncryption{Enabled: true},
		}, "no key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state, err := r.Execute(context.Background(), tc.job)
			require.ErrorContains(t, err, tc.wantErr)
			assert.Equal(t, "failed", state)
		})
	}
}

func TestRestoreExecuteGrantsMismatch(t *testing.T) {
	stub := &StubManagerClient{}
	stub.OnDownloadGrants = func(rid string, keys []string) (DownloadGrantsResponse, error) {
		return DownloadGrantsResponse{}, nil // short grant list
	}
	r := &Restorer{Manager: stub, WorkDir: t.TempDir()}
	state, err := r.Execute(context.Background(), RestoreJob{
		ID: "r-mm", Destination: RestoreDestination{Path: t.TempDir()},
		Objects: []RestoreObject{{Key: "a"}, {Key: "b"}},
	})
	require.ErrorContains(t, err, "want 2, got 0")
	assert.Equal(t, "failed", state)
}

func TestRestoreDownloadErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	r := &Restorer{Manager: &StubManagerClient{}, WorkDir: t.TempDir()}
	err := r.download(context.Background(), "r-dl", RestoreObject{Key: "k"},
		DownloadGrant{Key: "k", URL: srv.URL + "/o"}, filepath.Join(t.TempDir(), "o.bin"))
	require.ErrorContains(t, err, "403")

	err = r.download(context.Background(), "r-dl", RestoreObject{Key: "k"},
		DownloadGrant{Key: "k", URL: ""}, filepath.Join(t.TempDir(), "o.bin"))
	require.ErrorContains(t, err, "no URL")
}

func TestRestoreSizeMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("0123456789"))
	}))
	defer srv.Close()
	r := &Restorer{Manager: &StubManagerClient{}, WorkDir: t.TempDir()}
	err := r.download(context.Background(), "r-sz", RestoreObject{Key: "k", SizeBytes: 5},
		DownloadGrant{Key: "k", URL: srv.URL + "/o"}, filepath.Join(t.TempDir(), "o.bin"))
	require.ErrorContains(t, err, "size mismatch")
}

func TestWriteRestoreFileRefuseAndCap(t *testing.T) {
	out := t.TempDir()
	require.NoError(t, WriteRestoreFile(bytes.NewReader([]byte("data")), out, "d.sql", true, 1<<20))
	err := WriteRestoreFile(bytes.NewReader([]byte("data")), out, "d.sql", false, 1<<20)
	require.ErrorContains(t, err, "refusing to overwrite")
	err = WriteRestoreFile(bytes.NewReader(bytes.Repeat([]byte("x"), 100)), out, "big.sql", true, 10)
	require.ErrorContains(t, err, "exceeds")
}

func TestExtractTarEnforcesCap(t *testing.T) {
	raw := makeTarZst(t, map[string]string{"big.bin": string(bytes.Repeat([]byte("x"), 1024))})
	err := ExtractTar(decodeZst(t, raw), t.TempDir(), true, 16)
	require.ErrorContains(t, err, "exceeds")
}

func TestRestoreDbDumpWritesFile(t *testing.T) {
	dump := []byte("-- mysqldump output")
	var zst bytes.Buffer
	enc, err := zstd.NewWriter(&zst)
	require.NoError(t, err)
	_, err = enc.Write(dump)
	require.NoError(t, err)
	require.NoError(t, enc.Close())
	sum := sha256.Sum256(zst.Bytes())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(zst.Bytes())
	}))
	defer srv.Close()
	stub := &StubManagerClient{}
	stub.OnDownloadGrants = func(rid string, keys []string) (DownloadGrantsResponse, error) {
		return DownloadGrantsResponse{Grants: []DownloadGrant{{Key: keys[0], URL: srv.URL + "/o"}}}, nil
	}
	r := &Restorer{Manager: stub, WorkDir: t.TempDir()}
	dest := t.TempDir()
	state, err := r.Execute(context.Background(), RestoreJob{
		ID: "r-db", Kind: "mysql",
		Destination:     RestoreDestination{Mode: "path", Path: dest},
		OverwritePolicy: "allow",
		Objects:         []RestoreObject{{Key: "p/t0-mysql.sql.zst", SizeBytes: int64(zst.Len()), SHA256: hex.EncodeToString(sum[:])}},
	})
	require.NoError(t, err)
	assert.Equal(t, "succeeded", state)
	got, err := os.ReadFile(filepath.Join(dest, "t0-mysql.sql"))
	require.NoError(t, err)
	assert.Equal(t, dump, got)
}
