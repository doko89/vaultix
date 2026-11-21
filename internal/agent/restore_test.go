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
	_, err := r.restoreObject(context.Background(), job, job.Objects[0], DownloadGrant{Key: "k", URL: srv.URL + "/o"}, nil)
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

func TestNormalizeRestorePaths(t *testing.T) {
	got, err := normalizeRestorePaths(nil)
	require.NoError(t, err)
	assert.Empty(t, got)

	got, err = normalizeRestorePaths([]string{"/srv/data/a.txt", "/srv/data/", "/srv/data/a.txt", "data/b.txt"})
	require.NoError(t, err)
	assert.Equal(t, []string{"/srv/data/a.txt", "/srv/data", "data/b.txt"}, got)

	for _, bad := range []string{"", "   ", ".", "..", "../x", "./../x", "a/../../x"} {
		_, err := normalizeRestorePaths([]string{bad})
		require.Error(t, err, "path %q must be rejected", bad)
	}

	got, err = normalizeRestorePaths([]string{`C:\srv\data\a.txt`, `C:\srv\data`})
	require.NoError(t, err)
	assert.Equal(t, []string{"C:/srv/data/a.txt", "C:/srv/data"}, got)
}

func TestMatchRestoreWants(t *testing.T) {
	cases := []struct {
		entry string
		want  string
		hit   bool
	}{
		// Absolute wants (server contract) against base-prefixed entries.
		{"data/a.txt", "/srv/data/a.txt", true},
		{"data/sub/c.txt", "/srv/data/sub/c.txt", true},
		{"data/a.txt", "/srv/data", true},         // dir request covers subtree
		{"data/sub/c.txt", "/srv/data/sub", true}, // subdir request
		{"data/", "/srv/data", true},              // dir entry itself
		{"data/a.txt", "/srv/data/nope.txt", false},
		{"data/a.txt", "/other/root/a.txt", false},
		{"adata/a.txt", "/srv/data", false}, // segment alignment, not substring
		// Single-file snapshot: root file lands at archive top level.
		{"f.txt", "/srv/data/f.txt", true},
		// Archive-relative wants: exact + dir prefix.
		{"data/a.txt", "data/a.txt", true},
		{"data/a.txt", "data", true},
		{"data/a.txt", "dat", false},
		// Hostile entries never match anything.
		{"../evil", "/evil", false},
		{"../evil", "evil", false},
		{"/abs", "/abs", false},
		{"data/../../evil", "/evil", false},
		{"data/../../evil", "evil", false},
		// Windows-style wants match slash-form entries.
		{`data/a.txt`, `C:\srv\data\a.txt`, true},
		{`data/sub/c.txt`, `C:\srv\data`, true},
		{`data/a.txt`, `C:/srv/data/a.txt`, true},
		{`data/a.txt`, `D:\other\a.txt`, false},
		{`C:/evil`, `C:/evil`, false},
		{`C:\evil`, `C:\evil`, false},
	}
	for _, tc := range cases {
		hits := matchRestoreWants(tc.entry, []string{tc.want})
		if tc.hit {
			assert.Equal(t, []string{tc.want}, hits, "entry %q vs want %q", tc.entry, tc.want)
		} else {
			assert.Empty(t, hits, "entry %q vs want %q", tc.entry, tc.want)
		}
	}
}

func TestExtractTarSelectiveSingleFile(t *testing.T) {
	raw := makeTarZst(t, map[string]string{
		"data/a.txt":     "aaa",
		"data/b.txt":     "bbb",
		"data/sub/c.txt": "ccc",
	})
	out := filepath.Join(t.TempDir(), "out")
	matched, err := ExtractTarSelective(decodeZst(t, raw), out, false, 1<<20, []string{"/srv/data/sub/c.txt"})
	require.NoError(t, err)
	assert.Equal(t, []string{"/srv/data/sub/c.txt"}, matched)

	got, err := os.ReadFile(filepath.Join(out, "data", "sub", "c.txt"))
	require.NoError(t, err)
	assert.Equal(t, "ccc", string(got), "selected file bytes must match the archive")
	assert.NoFileExists(t, filepath.Join(out, "data", "a.txt"))
	assert.NoFileExists(t, filepath.Join(out, "data", "b.txt"))
}

func TestExtractTarSelectiveMultiPathSubset(t *testing.T) {
	raw := makeTarZst(t, map[string]string{
		"data/a.txt":     "aaa",
		"data/b.txt":     "bbb",
		"data/sub/c.txt": "ccc",
		"data/sub/d.txt": "ddd",
	})
	out := filepath.Join(t.TempDir(), "out")
	matched, err := ExtractTarSelective(decodeZst(t, raw), out, false, 1<<20,
		[]string{"/srv/data/a.txt", "/srv/data/sub"})
	require.NoError(t, err)
	assert.Equal(t, []string{"/srv/data/a.txt", "/srv/data/sub"}, matched)

	for _, f := range []string{"a.txt", "sub/c.txt", "sub/d.txt"} {
		assert.FileExists(t, filepath.Join(out, "data", f))
	}
	assert.NoFileExists(t, filepath.Join(out, "data", "b.txt"))
}

func TestExtractTarSelectiveDirRequestOnly(t *testing.T) {
	raw := makeTarZst(t, map[string]string{
		"data/keep.txt":  "keep",
		"data/sub/c.txt": "ccc",
	})
	out := filepath.Join(t.TempDir(), "out")
	matched, err := ExtractTarSelective(decodeZst(t, raw), out, false, 1<<20, []string{"data/sub"})
	require.NoError(t, err)
	assert.Equal(t, []string{"data/sub"}, matched)
	got, err := os.ReadFile(filepath.Join(out, "data", "sub", "c.txt"))
	require.NoError(t, err)
	assert.Equal(t, "ccc", string(got))
	assert.NoFileExists(t, filepath.Join(out, "data", "keep.txt"))
}

func TestExtractTarSelectiveTraversalNeverEscapes(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, TarZstd(&buf, 1, func(tw *tar.Writer) error {
		for name, content := range map[string]string{
			"../evil":              "bad",
			"/abs":                 "bad",
			"data/ok.txt":          "good",
			"data/sub/../../evil2": "bad",
		} {
			if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
				return err
			}
			if _, err := tw.Write([]byte(content)); err != nil {
				return err
			}
		}
		return nil
	}))
	root := t.TempDir()
	out := filepath.Join(root, "out")
	matched, err := ExtractTarSelective(decodeZst(t, buf.Bytes()), out, false, 1<<20, []string{"/src/data/ok.txt"})
	require.NoError(t, err, "hostile entries outside the selection must be skipped, not fatal")
	assert.Equal(t, []string{"/src/data/ok.txt"}, matched)

	got, err := os.ReadFile(filepath.Join(out, "data", "ok.txt"))
	require.NoError(t, err)
	assert.Equal(t, "good", string(got))
	assert.NoFileExists(t, filepath.Join(root, "evil"), "traversal entry must not escape the destination")
	assert.NoFileExists(t, filepath.Join(out, "evil2"))
	entries, err := os.ReadDir(out)
	require.NoError(t, err)
	require.Len(t, entries, 1, "only the selected subtree may be materialized")
	assert.Equal(t, "data", entries[0].Name())
}

func TestExtractTarSelectiveNilWantsIsFullRestore(t *testing.T) {
	raw := makeTarZst(t, map[string]string{"a.txt": "hello", "sub/b.txt": "world"})
	out := filepath.Join(t.TempDir(), "out")
	matched, err := ExtractTarSelective(decodeZst(t, raw), out, false, 1<<20, nil)
	require.NoError(t, err)
	assert.Empty(t, matched)
	got, err := os.ReadFile(filepath.Join(out, "a.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello", string(got))
	got, err = os.ReadFile(filepath.Join(out, "sub", "b.txt"))
	require.NoError(t, err)
	assert.Equal(t, "world", string(got))
}

func selectiveStub(t *testing.T, payload []byte, reported *RestoreReportRequest) *StubManagerClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	t.Cleanup(srv.Close)
	stub := &StubManagerClient{}
	stub.OnDownloadGrants = func(rid string, keys []string) (DownloadGrantsResponse, error) {
		return DownloadGrantsResponse{Grants: []DownloadGrant{{Key: keys[0], URL: srv.URL + "/o"}}}, nil
	}
	stub.OnReportRestore = func(rid string, req RestoreReportRequest) error {
		*reported = req
		return nil
	}
	return stub
}

func TestRestoreExecuteSelectiveEndToEnd(t *testing.T) {
	payload := makeTarZst(t, map[string]string{
		"data/a.txt":     "selected-bytes",
		"data/other.txt": "not-wanted",
	})
	sum := sha256.Sum256(payload)
	var reported RestoreReportRequest
	stub := selectiveStub(t, payload, &reported)
	r := &Restorer{Manager: stub, WorkDir: t.TempDir()}
	dest := t.TempDir()
	state, err := r.Execute(context.Background(), RestoreJob{
		ID: "r-sel", SnapshotID: "s-1", Kind: "filesystem",
		Destination:     RestoreDestination{Mode: "path", Path: dest},
		OverwritePolicy: "allow",
		Paths:           []string{"/srv/data/a.txt"},
		Objects:         []RestoreObject{{Key: "k", SizeBytes: int64(len(payload)), SHA256: hex.EncodeToString(sum[:])}},
	})
	require.NoError(t, err)
	assert.Equal(t, "succeeded", state)
	assert.Equal(t, "succeeded", reported.State)
	assert.Equal(t, []string{"/srv/data/a.txt"}, reported.RestoredPaths)
	assert.Empty(t, reported.MissingPaths)
	got, err := os.ReadFile(filepath.Join(dest, "data", "a.txt"))
	require.NoError(t, err)
	assert.Equal(t, "selected-bytes", string(got))
	assert.NoFileExists(t, filepath.Join(dest, "data", "other.txt"))
}

func TestRestoreExecuteSelectiveSkipsManifestObject(t *testing.T) {
	// Live shape: the manager dispatches Kind "file" with the data object
	// plus the manifest.json sidecar. Selective restore must skip the
	// metadata object (no grant, no download) instead of failing on it.
	payload := makeTarZst(t, map[string]string{
		"data/a.txt":     "selected-bytes",
		"data/other.txt": "not-wanted",
	})
	sum := sha256.Sum256(payload)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	t.Cleanup(srv.Close)
	var reported RestoreReportRequest
	var granted []string
	stub := &StubManagerClient{}
	stub.OnDownloadGrants = func(rid string, keys []string) (DownloadGrantsResponse, error) {
		granted = append(granted, keys...)
		out := DownloadGrantsResponse{}
		for _, k := range keys {
			out.Grants = append(out.Grants, DownloadGrant{Key: k, URL: srv.URL + "/o"})
		}
		return out, nil
	}
	stub.OnReportRestore = func(rid string, req RestoreReportRequest) error {
		reported = req
		return nil
	}
	r := &Restorer{Manager: stub, WorkDir: t.TempDir()}
	dest := t.TempDir()
	manifestPayload := []byte(`{"objects":[]}`)
	manifestSum := sha256.Sum256(manifestPayload)
	state, err := r.Execute(context.Background(), RestoreJob{
		ID: "r-sel-manifest", SnapshotID: "s-1", Kind: "file",
		Destination:     RestoreDestination{Mode: "path", Path: dest},
		OverwritePolicy: "allow",
		Paths:           []string{"/srv/data/a.txt"},
		Objects: []RestoreObject{
			{Key: "jobs/r-sel-manifest/t0-host_filesystem.tar.zst", SizeBytes: int64(len(payload)), SHA256: hex.EncodeToString(sum[:])},
			{Key: "jobs/r-sel-manifest/manifest.json", SizeBytes: int64(len(manifestPayload)), SHA256: hex.EncodeToString(manifestSum[:])},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "succeeded", state)
	assert.Equal(t, "succeeded", reported.State)
	assert.Equal(t, []string{"/srv/data/a.txt"}, reported.RestoredPaths)
	assert.Empty(t, reported.MissingPaths)
	assert.Equal(t, []string{"jobs/r-sel-manifest/t0-host_filesystem.tar.zst"}, granted)
	got, err := os.ReadFile(filepath.Join(dest, "data", "a.txt"))
	require.NoError(t, err)
	assert.Equal(t, "selected-bytes", string(got))
	assert.NoFileExists(t, filepath.Join(dest, "data", "other.txt"))
	assert.NoFileExists(t, filepath.Join(dest, "manifest.json"))
}

func TestRestoreExecuteSelectiveMissingFails(t *testing.T) {
	payload := makeTarZst(t, map[string]string{"data/a.txt": "aaa"})
	sum := sha256.Sum256(payload)
	var reported RestoreReportRequest
	stub := selectiveStub(t, payload, &reported)
	r := &Restorer{Manager: stub, WorkDir: t.TempDir()}
	state, err := r.Execute(context.Background(), RestoreJob{
		ID: "r-miss", SnapshotID: "s-1", Kind: "filesystem",
		Destination:     RestoreDestination{Mode: "path", Path: t.TempDir()},
		OverwritePolicy: "allow",
		Paths:           []string{"/srv/data/a.txt", "/srv/data/nope.txt", "/srv/data/gone/deep.txt"},
		Objects:         []RestoreObject{{Key: "k", SizeBytes: int64(len(payload)), SHA256: hex.EncodeToString(sum[:])}},
	})
	require.Error(t, err)
	assert.Equal(t, "failed", state)
	assert.Equal(t, "failed", reported.State)
	assert.Contains(t, err.Error(), "/srv/data/nope.txt")
	assert.Contains(t, err.Error(), "/srv/data/gone/deep.txt")
	assert.Contains(t, reported.Error, "not found in snapshot")
	assert.Equal(t, []string{"/srv/data/a.txt"}, reported.RestoredPaths)
	assert.Equal(t, []string{"/srv/data/nope.txt", "/srv/data/gone/deep.txt"}, reported.MissingPaths)
}

func TestRestoreExecuteSelectiveRejectsBadPaths(t *testing.T) {
	stub := &StubManagerClient{}
	var reported RestoreReportRequest
	stub.OnReportRestore = func(rid string, req RestoreReportRequest) error {
		reported = req
		return nil
	}
	r := &Restorer{Manager: stub, WorkDir: t.TempDir()}
	state, err := r.Execute(context.Background(), RestoreJob{
		ID: "r-bad", Kind: "filesystem",
		Destination:     RestoreDestination{Mode: "path", Path: t.TempDir()},
		OverwritePolicy: "allow",
		Paths:           []string{"../escape"},
		Objects:         []RestoreObject{{Key: "k"}},
	})
	require.ErrorContains(t, err, "escape the snapshot root")
	assert.Equal(t, "failed", state)
	assert.Equal(t, "failed", reported.State)
}

func TestRestoreExecuteSelectiveRefusedForDbDumps(t *testing.T) {
	dump := []byte("-- mysqldump output")
	var zst bytes.Buffer
	enc, err := zstd.NewWriter(&zst)
	require.NoError(t, err)
	_, err = enc.Write(dump)
	require.NoError(t, err)
	require.NoError(t, enc.Close())
	sum := sha256.Sum256(zst.Bytes())
	var reported RestoreReportRequest
	stub := selectiveStub(t, zst.Bytes(), &reported)
	r := &Restorer{Manager: stub, WorkDir: t.TempDir()}
	state, err := r.Execute(context.Background(), RestoreJob{
		ID: "r-dbsel", Kind: "mysql",
		Destination:     RestoreDestination{Mode: "path", Path: t.TempDir()},
		OverwritePolicy: "allow",
		Paths:           []string{"/var/lib/mysql/appdb/users.sql"},
		Objects:         []RestoreObject{{Key: "p/t0-mysql.sql.zst", SizeBytes: int64(zst.Len()), SHA256: hex.EncodeToString(sum[:])}},
	})
	require.ErrorContains(t, err, "selective restore")
	assert.Equal(t, "failed", state)
	assert.Equal(t, "failed", reported.State)
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
