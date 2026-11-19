package agent

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTarZstdRoundTrip(t *testing.T) {
	srcDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "a.txt"), []byte("hello vaultix"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(srcDir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "sub", "b.bin"), bytes.Repeat([]byte{0xAB}, 4096), 0o644))

	var archive bytes.Buffer
	err := TarZstd(&archive, 3, func(tw *tar.Writer) error {
		if err := AddFileToTar(tw, filepath.Join(srcDir, "a.txt"), "a.txt"); err != nil {
			return err
		}
		return AddFileToTar(tw, filepath.Join(srcDir, "sub", "b.bin"), "sub/b.bin")
	})
	require.NoError(t, err)
	require.NotZero(t, archive.Len())

	destDir := t.TempDir()
	require.NoError(t, UntarZstd(&archive, destDir, 1<<20))

	got, err := os.ReadFile(filepath.Join(destDir, "a.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello vaultix", string(got))
	gotBin, err := os.ReadFile(filepath.Join(destDir, "sub", "b.bin"))
	require.NoError(t, err)
	assert.Equal(t, bytes.Repeat([]byte{0xAB}, 4096), gotBin)
}

func TestUntarZstdRejectsTraversal(t *testing.T) {
	var archive bytes.Buffer
	require.NoError(t, TarZstd(&archive, 1, func(tw *tar.Writer) error {
		if err := tw.WriteHeader(&tar.Header{Name: "../evil.txt", Mode: 0o644, Size: 3, Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err := tw.Write([]byte("bad"))
		return err
	}))
	err := UntarZstd(&archive, t.TempDir(), 1<<20)
	require.ErrorContains(t, err, "escapes destination")
}

func TestUntarZstdEnforcesCap(t *testing.T) {
	var archive bytes.Buffer
	payload := bytes.Repeat([]byte("x"), 1024)
	require.NoError(t, TarZstd(&archive, 1, func(tw *tar.Writer) error {
		if err := tw.WriteHeader(&tar.Header{Name: "big.bin", Mode: 0o644, Size: int64(len(payload)), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err := tw.Write(payload)
		return err
	}))
	err := UntarZstd(&archive, t.TempDir(), 16)
	require.ErrorContains(t, err, "exceeds")
}

func TestAddFileToTarRejectsNonRegular(t *testing.T) {
	var archive bytes.Buffer
	err := TarZstd(&archive, 1, func(tw *tar.Writer) error {
		return AddFileToTar(tw, t.TempDir(), "dir")
	})
	require.ErrorContains(t, err, "not a regular file")
}
