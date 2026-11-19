package agent

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// DefaultArchiveMaxBytes bounds extraction (1 TiB); callers with tighter
// knowledge of the dataset should pass a smaller cap.
const DefaultArchiveMaxBytes = 1 << 40

// TarZstd streams a tar archive compressed with zstd to w. The fill callback
// writes entries to tw; data is streamed through and never fully buffered.
// level follows zstd levels (1-22); out-of-range values use the default.
func TarZstd(w io.Writer, level int, fill func(tw *tar.Writer) error) error {
	encLevel := zstd.EncoderLevelFromZstd(level)
	if level < 1 || level > 22 {
		encLevel = zstd.EncoderLevelFromZstd(3)
	}
	enc, err := zstd.NewWriter(w, zstd.WithEncoderLevel(encLevel))
	if err != nil {
		return fmt.Errorf("agent: create zstd encoder: %w", err)
	}
	tw := tar.NewWriter(enc)
	if err := fill(tw); err != nil {
		_ = tw.Close()
		_ = enc.Close()
		return err
	}
	if err := tw.Close(); err != nil {
		_ = enc.Close()
		return fmt.Errorf("agent: close tar writer: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("agent: close zstd encoder: %w", err)
	}
	return nil
}

// AddFileToTar streams a single regular file into the archive under arcName.
// The file content is copied in chunks; it is never loaded whole.
func AddFileToTar(tw *tar.Writer, srcPath, arcName string) error {
	fi, err := os.Stat(srcPath)
	if err != nil {
		return fmt.Errorf("agent: stat %s: %w", srcPath, err)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("agent: not a regular file: %s", srcPath)
	}
	f, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("agent: open %s: %w", srcPath, err)
	}
	defer func() { _ = f.Close() }()
	hdr, err := tar.FileInfoHeader(fi, "")
	if err != nil {
		return fmt.Errorf("agent: tar header for %s: %w", srcPath, err)
	}
	hdr.Name = arcName
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("agent: write tar header: %w", err)
	}
	if _, err := io.Copy(tw, f); err != nil {
		return fmt.Errorf("agent: stream %s: %w", srcPath, err)
	}
	return nil
}

// UntarZstd extracts a tar+zstd stream into destDir, streaming entry by
// entry. It rejects path traversal, absolute paths, and non-regular files,
// and aborts past maxTotalBytes of extracted content.
func UntarZstd(r io.Reader, destDir string, maxTotalBytes int64) error {
	if maxTotalBytes <= 0 {
		maxTotalBytes = DefaultArchiveMaxBytes
	}
	dec, err := zstd.NewReader(r)
	if err != nil {
		return fmt.Errorf("agent: create zstd decoder: %w", err)
	}
	defer dec.Close()
	tr := tar.NewReader(dec)
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("agent: read tar entry: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		target, err := safeJoin(destDir, hdr.Name)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("agent: create dir: %w", err)
		}
		//nolint:gosec // target is confined to destDir by safeJoin.
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, hdr.FileInfo().Mode().Perm())
		if err != nil {
			return fmt.Errorf("agent: create %s: %w", target, err)
		}
		n, copyErr := io.CopyN(f, tr, maxTotalBytes-total+1)
		total += n
		closeErr := f.Close()
		if copyErr != nil && copyErr != io.EOF {
			return fmt.Errorf("agent: extract %s: %w", hdr.Name, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("agent: close %s: %w", target, closeErr)
		}
		if total > maxTotalBytes {
			return fmt.Errorf("agent: archive exceeds %d bytes", maxTotalBytes)
		}
	}
}

func safeJoin(destDir, name string) (string, error) {
	if filepath.IsAbs(name) {
		return "", fmt.Errorf("agent: archive entry with absolute path: %q", name)
	}
	target := filepath.Join(destDir, name)
	rel, err := filepath.Rel(destDir, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("agent: archive entry escapes destination: %q", name)
	}
	return target, nil
}
