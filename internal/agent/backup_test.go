package agent

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/moby/moby/api/types/volume"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// listTarZstd decodes a tar+zstd stream into name->content for assertions.
func listTarZstd(t *testing.T, raw []byte) map[string]tar.Header {
	t.Helper()
	dec, err := zstd.NewReader(bytes.NewReader(raw))
	require.NoError(t, err)
	defer dec.Close()
	tr := tar.NewReader(dec)
	out := map[string]tar.Header{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return out
		}
		require.NoError(t, err)
		out[hdr.Name] = *hdr
		_, err = io.Copy(io.Discard, tr)
		require.NoError(t, err)
	}
}

func writeTestTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "keep.txt"), []byte("keep"), 0o640))
	require.NoError(t, os.WriteFile(filepath.Join(root, "skip.log"), []byte("skip"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "sub", "data.bin"), []byte("data"), 0o600))
	require.NoError(t, os.Symlink("keep.txt", filepath.Join(root, "link.txt")))
	require.NoError(t, os.Symlink("sub", filepath.Join(root, "dirlink")))
	return root
}

func TestFilesystemRoundTripGlobs(t *testing.T) {
	root := writeTestTree(t)
	var buf bytes.Buffer
	n, err := BackupFilesystem(context.Background(), &buf, root, FilesystemOptions{
		Include: []string{"*.txt"},
		Exclude: []string{"skip*"},
		Symlink: SymlinkSkip,
	}, 1)
	require.NoError(t, err)
	assert.Equal(t, int64(len("keep")), n, "only keep.txt content is read")

	entries := listTarZstd(t, buf.Bytes())
	base := filepath.Base(root)
	assert.Contains(t, entries, base+"/keep.txt")
	assert.NotContains(t, entries, base+"/skip.log")
	assert.NotContains(t, entries, base+"/sub/data.bin")
	assert.NotContains(t, entries, base+"/link.txt")
}

func TestFilesystemExcludeWins(t *testing.T) {
	root := writeTestTree(t)
	var buf bytes.Buffer
	_, err := BackupFilesystem(context.Background(), &buf, root, FilesystemOptions{Exclude: []string{"*"}}, 1)
	require.NoError(t, err)
	assert.Empty(t, listTarZstd(t, buf.Bytes()))
}

func TestFilesystemSymlinkPolicies(t *testing.T) {
	root := writeTestTree(t)
	base := filepath.Base(root)

	var preserved bytes.Buffer
	_, err := BackupFilesystem(context.Background(), &preserved, root, FilesystemOptions{Symlink: SymlinkPreserve}, 1)
	require.NoError(t, err)
	entries := listTarZstd(t, preserved.Bytes())
	link := entries[base+"/link.txt"]
	assert.Equal(t, byte(tar.TypeSymlink), link.Typeflag)
	assert.Equal(t, "keep.txt", link.Linkname)

	var followed bytes.Buffer
	_, err = BackupFilesystem(context.Background(), &followed, root, FilesystemOptions{Symlink: SymlinkFollow}, 1)
	require.NoError(t, err)
	entries = listTarZstd(t, followed.Bytes())
	link = entries[base+"/link.txt"]
	assert.Equal(t, byte(tar.TypeReg), link.Typeflag, "followed link archives target content")

	var skipped bytes.Buffer
	_, err = BackupFilesystem(context.Background(), &skipped, root, FilesystemOptions{Symlink: SymlinkSkip}, 1)
	require.NoError(t, err)
	assert.NotContains(t, listTarZstd(t, skipped.Bytes()), base+"/link.txt")
}

func TestFilesystemPreservesMetadata(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "f.txt")
	require.NoError(t, os.WriteFile(p, []byte("x"), 0o640))
	mtime := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	require.NoError(t, os.Chtimes(p, mtime, mtime))

	var buf bytes.Buffer
	_, err := BackupFilesystem(context.Background(), &buf, p, FilesystemOptions{}, 1)
	require.NoError(t, err)
	entries := listTarZstd(t, buf.Bytes())
	hdr := entries["f.txt"]
	assert.Equal(t, int64(0o640), int64(hdr.Mode&0o777))
	assert.True(t, hdr.ModTime.Equal(mtime), "mtime preserved, got %v", hdr.ModTime)
	assert.Equal(t, int64(1), hdr.Size)
}

func TestFilesystemMissingRoot(t *testing.T) {
	var buf bytes.Buffer
	_, err := BackupFilesystem(context.Background(), &buf, filepath.Join(t.TempDir(), "nope"), FilesystemOptions{}, 1)
	require.ErrorContains(t, err, "stat")
}

func TestParseFilesystemOptionsRejectsBadSymlink(t *testing.T) {
	_, err := ParseFilesystemOptions(map[string]any{"symlink": "explode"})
	require.ErrorContains(t, err, "symlink")
	_, err = ParseFilesystemOptions(map[string]any{"include": "notalist"})
	require.ErrorContains(t, err, "include")
	opts, err := ParseFilesystemOptions(nil)
	require.NoError(t, err)
	assert.Equal(t, SymlinkPreserve, opts.Symlink)
}

func TestDockerVolumeBackup(t *testing.T) {
	// No daemon here: fake resolver points the volume at a temp dir.
	data := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(data, "pg.bin"), []byte("voldata"), 0o644))
	resolver := stubVolumeResolver{vol: DiscoveredVolume{
		Name: "pgdata", Driver: "local", Mountpoint: data, Labels: map[string]string{"app": "db"},
	}}
	var buf bytes.Buffer
	n, err := BackupDockerVolume(t.Context(), &buf, "pgdata", DockerVolumeOptions{}, 1, resolver)
	require.NoError(t, err)
	assert.Equal(t, int64(len("voldata")), n)
	entries := listTarZstd(t, buf.Bytes())
	assert.Contains(t, entries, "vaultix-volume.json")
	assert.Contains(t, entries, "pg.bin")
	assert.Contains(t, readTarEntry(t, buf.Bytes(), "vaultix-volume.json"), `"name":"pgdata"`)
	assert.Contains(t, readTarEntry(t, buf.Bytes(), "vaultix-volume.json"), `"driver":"local"`)
}

// readTarEntry returns the content of one entry in a tar+zstd stream.
func readTarEntry(t *testing.T, raw []byte, name string) string {
	t.Helper()
	dec, err := zstd.NewReader(bytes.NewReader(raw))
	require.NoError(t, err)
	defer dec.Close()
	tr := tar.NewReader(dec)
	for {
		hdr, err := tr.Next()
		require.NoError(t, err)
		content, err := io.ReadAll(tr)
		require.NoError(t, err)
		if hdr.Name == name {
			return string(content)
		}
	}
}

func TestDockerVolumeMissing(t *testing.T) {
	resolver := stubVolumeResolver{err: assert.AnError}
	var buf bytes.Buffer
	_, err := BackupDockerVolume(t.Context(), &buf, "ghost", DockerVolumeOptions{}, 1, resolver)
	require.Error(t, err)
}

func TestDockerVolumeResolver(t *testing.T) {
	fake := &fakeLister{volumes: []volume.Volume{{
		Name: "shop_data", Driver: "local", Mountpoint: "/mnt/shop_data",
	}}}
	r := &DockerVolumeResolver{Lister: fake}
	vol, err := r.ResolveVolume(context.Background(), "shop_data")
	require.NoError(t, err)
	assert.Equal(t, "/mnt/shop_data", vol.Mountpoint)
	_, err = r.ResolveVolume(context.Background(), "ghost")
	require.ErrorContains(t, err, "not found")

	_, err = (&DockerVolumeResolver{}).ResolveVolume(context.Background(), "x")
	require.ErrorContains(t, err, "no docker client")
	var nilRes *DockerVolumeResolver
	_, err = nilRes.ResolveVolume(context.Background(), "x")
	require.ErrorContains(t, err, "no docker client")
}

func TestParseDockerVolumeOptions(t *testing.T) {
	opts, err := ParseDockerVolumeOptions(map[string]any{"include": []any{"*.db"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"*.db"}, opts.Include)
	assert.Equal(t, SymlinkPreserve, opts.Symlink)
	_, err = ParseDockerVolumeOptions(map[string]any{"symlink": "bogus"})
	require.Error(t, err)
}

func TestStringListRejectsNonStringElement(t *testing.T) {
	_, err := ParseFilesystemOptions(map[string]any{"exclude": []any{"ok", 42}})
	require.ErrorContains(t, err, "exclude[1]")
}

type stubVolumeResolver struct {
	vol DiscoveredVolume
	err error
}

func (s stubVolumeResolver) ResolveVolume(_ context.Context, name string) (DiscoveredVolume, error) {
	if s.err != nil {
		return DiscoveredVolume{}, s.err
	}
	if name != s.vol.Name {
		return DiscoveredVolume{}, assert.AnError
	}
	return s.vol, nil
}
