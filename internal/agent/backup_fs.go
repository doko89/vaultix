package agent

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Symlink policies for filesystem backup.
const (
	SymlinkPreserve = "preserve" // store the link itself (default)
	SymlinkFollow   = "follow"   // archive the target's content
	SymlinkSkip     = "skip"     // ignore symlinks
)

// FilesystemOptions configures a host_filesystem target backup.
type FilesystemOptions struct {
	Include []string // glob patterns (path.Match); empty = everything
	Exclude []string // glob patterns; exclude wins
	Symlink string   // preserve (default) | follow | skip
}

// ParseFilesystemOptions decodes target options. Unknown keys are ignored;
// unknown symlink values are rejected.
func ParseFilesystemOptions(opts map[string]any) (FilesystemOptions, error) {
	var out FilesystemOptions
	var err error
	if out.Include, err = stringList(opts, "include"); err != nil {
		return out, err
	}
	if out.Exclude, err = stringList(opts, "exclude"); err != nil {
		return out, err
	}
	out.Symlink = SymlinkPreserve
	if raw, ok := opts["symlink"]; ok {
		s, ok := raw.(string)
		if !ok {
			return out, fmt.Errorf("agent: fs option symlink must be a string")
		}
		switch strings.ToLower(strings.TrimSpace(s)) {
		case SymlinkPreserve, SymlinkFollow, SymlinkSkip:
			out.Symlink = strings.ToLower(strings.TrimSpace(s))
		default:
			return out, fmt.Errorf("agent: fs option symlink must be preserve|follow|skip, got %q", s)
		}
	}
	return out, nil
}

func stringList(opts map[string]any, key string) ([]string, error) {
	raw, ok := opts[key]
	if !ok || raw == nil {
		return nil, nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("agent: fs option %s must be a list of strings", key)
	}
	out := make([]string, 0, len(arr))
	for i, v := range arr {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("agent: fs option %s[%d] must be a string", key, i)
		}
		out = append(out, s)
	}
	return out, nil
}

// fsStats accumulates backup counters.
type fsStats struct {
	bytesRead int64
	files     int64
}

// BackupFilesystem streams root (file or directory) as tar+zstd to w,
// honoring include/exclude globs and the symlink policy. Only paths under
// root are read — never anything else. Returns bytes read (pre-compression).
func BackupFilesystem(ctx context.Context, w io.Writer, root string, opts FilesystemOptions, level int) (bytesRead int64, err error) {
	var n int64
	if err := TarZstd(w, level, func(tw *tar.Writer) error {
		var werr error
		n, werr = writeFSTar(ctx, tw, root, opts)
		return werr
	}); err != nil {
		return n, err
	}
	return n, nil
}

// writeFSTar writes the tar entries for root to an existing tar writer and
// returns bytes read. The executor uses it to own the compression layer.
// The walk aborts promptly when ctx is cancelled.
func writeFSTar(ctx context.Context, tw *tar.Writer, root string, opts FilesystemOptions) (int64, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return 0, fmt.Errorf("agent: fs backup: stat %s: %w", root, err)
	}
	var st fsStats
	if !info.IsDir() {
		if err := addFSEntry(tw, root, info, filepath.Base(root), opts, &st); err != nil {
			return st.bytesRead, err
		}
		return st.bytesRead, nil
	}
	base := filepath.Base(root)
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			return fmt.Errorf("agent: fs backup: walk %s: %w", path, err)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		arcName := filepath.ToSlash(filepath.Join(base, rel))
		fi, err := d.Info()
		if err != nil {
			return fmt.Errorf("agent: fs backup: stat %s: %w", path, err)
		}
		return addFSEntry(tw, path, fi, arcName, opts, &st)
	})
	return st.bytesRead, err
}

func addFSEntry(tw *tar.Writer, path string, fi fs.FileInfo, arcName string, opts FilesystemOptions, st *fsStats) error {
	if !matchGlobs(arcName, fi.IsDir(), opts.Include, opts.Exclude) {
		if fi.IsDir() {
			return filepath.SkipDir
		}
		return nil
	}
	mode := fi.Mode()
	switch {
	case mode.IsDir():
		hdr, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			return fmt.Errorf("agent: fs backup: tar header for %s: %w", path, err)
		}
		hdr.Name = arcName + "/"
		// mod-time/size/perm metadata ride on every header.
		if err := tw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("agent: fs backup: write header: %w", err)
		}
		return nil
	case mode.IsRegular():
		if err := writeFileEntry(tw, path, fi, arcName); err != nil {
			return err
		}
		st.bytesRead += fi.Size()
		st.files++
		return nil
	case mode&fs.ModeSymlink != 0:
		return addSymlinkEntry(tw, path, fi, arcName, opts.Symlink, st)
	default:
		// Sockets, devices, fifos: never archived (could block or leak).
		return nil
	}
}

func writeFileEntry(tw *tar.Writer, path string, fi fs.FileInfo, arcName string) error {
	hdr, err := tar.FileInfoHeader(fi, "")
	if err != nil {
		return fmt.Errorf("agent: fs backup: tar header for %s: %w", path, err)
	}
	hdr.Name = arcName
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("agent: fs backup: write header: %w", err)
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("agent: fs backup: open %s: %w", path, err)
	}
	n, err := io.Copy(tw, f)
	cerr := f.Close()
	if err != nil {
		return fmt.Errorf("agent: fs backup: stream %s: %w", path, err)
	}
	if cerr != nil {
		return fmt.Errorf("agent: fs backup: close %s: %w", path, cerr)
	}
	if n != fi.Size() {
		return fmt.Errorf("agent: fs backup: %s changed during read (stat %d, read %d)", path, fi.Size(), n)
	}
	return nil
}

func addSymlinkEntry(tw *tar.Writer, path string, fi fs.FileInfo, arcName, policy string, st *fsStats) error {
	switch policy {
	case SymlinkSkip:
		return nil
	case SymlinkFollow:
		target, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("agent: fs backup: follow symlink %s: %w", path, err)
		}
		if !target.Mode().IsRegular() {
			return nil
		}
		if err := writeFileEntry(tw, path, target, arcName); err != nil {
			return err
		}
		st.bytesRead += target.Size()
		st.files++
		return nil
	default: // preserve
		linkTarget, err := os.Readlink(path)
		if err != nil {
			return fmt.Errorf("agent: fs backup: readlink %s: %w", path, err)
		}
		hdr, err := tar.FileInfoHeader(fi, linkTarget)
		if err != nil {
			return fmt.Errorf("agent: fs backup: tar header for %s: %w", path, err)
		}
		hdr.Name = arcName
		if err := tw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("agent: fs backup: write header: %w", err)
		}
		return nil
	}
}

// matchGlobs reports whether arcName passes include/exclude filtering.
// Patterns use path.Match against the full archive name and the base name;
// exclude wins over include.
func matchGlobs(arcName string, isDir bool, include, exclude []string) bool {
	for _, pat := range exclude {
		if globMatch(pat, arcName, isDir) {
			return false
		}
	}
	if len(include) == 0 {
		return true
	}
	// Directories on the way to an included file must be kept so the walk
	// descends; a dir passes when any include pattern could match below it.
	for _, pat := range include {
		if globMatch(pat, arcName, isDir) {
			return true
		}
		if isDir && maybeBelow(pat, arcName) {
			return true
		}
	}
	return false
}

func globMatch(pat, arcName string, isDir bool) bool {
	for _, cand := range []string{arcName, filepath.Base(arcName)} {
		if ok, _ := filepath.Match(pat, cand); ok {
			return true
		}
		// "dir/*" style patterns also match the dir itself.
		if isDir {
			if ok, _ := filepath.Match(pat, cand+"/x"); ok {
				return true
			}
		}
	}
	return false
}

// maybeBelow reports whether pattern pat might match something under dir.
// It treats the pattern's literal prefix (up to the first meta char) as a
// path hint: if dir is that hint or an ancestor of it, descend.
func maybeBelow(pat, dir string) bool {
	meta := strings.IndexAny(pat, "*?[\\")
	literal := pat
	if meta >= 0 {
		literal = pat[:meta]
	}
	literal = strings.TrimSuffix(literal, "/")
	if literal == "" {
		return true
	}
	return dir == literal || strings.HasPrefix(literal, dir+"/")
}
