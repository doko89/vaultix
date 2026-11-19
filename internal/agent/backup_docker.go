package agent

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// DockerVolumeOptions configures a docker_volume target backup.
type DockerVolumeOptions struct {
	Include []string // same glob semantics as filesystem backup
	Exclude []string
	Symlink string
}

// ParseDockerVolumeOptions decodes target options.
func ParseDockerVolumeOptions(opts map[string]any) (DockerVolumeOptions, error) {
	fsOpts, err := ParseFilesystemOptions(opts)
	if err != nil {
		return DockerVolumeOptions{}, err
	}
	return DockerVolumeOptions{Include: fsOpts.Include, Exclude: fsOpts.Exclude, Symlink: fsOpts.Symlink}, nil
}

// volumeMetadata is stored as the first archive entry (vaultix-volume.json)
// so a restore can show which volume, driver, and labels produced the data.
type volumeMetadata struct {
	Name       string            `json:"name"`
	Driver     string            `json:"driver"`
	Mountpoint string            `json:"mountpoint"`
	Labels     map[string]string `json:"labels,omitempty"`
	BackedUpAt string            `json:"backed_up_at"`
}

// VolumeResolver maps a volume name to its inspected metadata. In production
// it lists volumes through the Docker Engine client and finds the named one.
type VolumeResolver interface {
	ResolveVolume(ctx context.Context, name string) (DiscoveredVolume, error)
}

// DockerVolumeResolver implements VolumeResolver over a DockerLister.
type DockerVolumeResolver struct {
	Lister DockerLister
}

// ResolveVolume finds the named volume via VolumeList.
func (r *DockerVolumeResolver) ResolveVolume(ctx context.Context, name string) (DiscoveredVolume, error) {
	if r == nil || r.Lister == nil {
		return DiscoveredVolume{}, fmt.Errorf("agent: docker backup: no docker client configured (enable docker in agent config)")
	}
	report, err := NewDiscoverer(true, r.Lister, 0).Discover(ctx)
	if err != nil {
		return DiscoveredVolume{}, fmt.Errorf("agent: docker backup: list volumes: %w", err)
	}
	for _, v := range report.Volumes {
		if v.Name == name {
			return v, nil
		}
	}
	return DiscoveredVolume{}, fmt.Errorf("agent: docker backup: volume %q not found", name)
}

// BackupDockerVolume streams the named volume's Mountpoint as tar+zstd to w.
// The first archive entry is vaultix-volume.json (config metadata); the rest
// is the volume data tree with the same glob/symlink semantics as filesystem
// backup. Returns bytes read (pre-compression, excluding the metadata file).
func BackupDockerVolume(ctx context.Context, w io.Writer, volumeName string, opts DockerVolumeOptions, level int, resolver VolumeResolver) (bytesRead int64, err error) {
	var n int64
	if err := TarZstd(w, level, func(tw *tar.Writer) error {
		var werr error
		n, werr = writeDockerTar(ctx, tw, volumeName, opts, resolver)
		return werr
	}); err != nil {
		return n, err
	}
	return n, nil
}

// writeDockerTar writes the metadata entry plus the volume data tree to an
// existing tar writer. The executor uses it to own the compression layer.
func writeDockerTar(ctx context.Context, tw *tar.Writer, volumeName string, opts DockerVolumeOptions, resolver VolumeResolver) (int64, error) {
	if resolver == nil {
		return 0, fmt.Errorf("agent: docker backup: no volume resolver configured")
	}
	vol, err := resolver.ResolveVolume(ctx, volumeName)
	if err != nil {
		return 0, err
	}
	if vol.Mountpoint == "" {
		return 0, fmt.Errorf("agent: docker backup: volume %q has no mountpoint", volumeName)
	}
	meta, err := json.Marshal(volumeMetadata{
		Name: vol.Name, Driver: vol.Driver, Mountpoint: vol.Mountpoint,
		Labels: vol.Labels, BackedUpAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return 0, fmt.Errorf("agent: docker backup: encode metadata: %w", err)
	}
	if err := tw.WriteHeader(&tar.Header{
		Name: "vaultix-volume.json", Mode: 0o644, Size: int64(len(meta)),
		Typeflag: tar.TypeReg, ModTime: time.Now().UTC(),
	}); err != nil {
		return 0, fmt.Errorf("agent: docker backup: write metadata header: %w", err)
	}
	if _, err := tw.Write(meta); err != nil {
		return 0, fmt.Errorf("agent: docker backup: write metadata: %w", err)
	}
	fsOpts := FilesystemOptions{Include: opts.Include, Exclude: opts.Exclude, Symlink: opts.Symlink}
	var st fsStats
	info, err := os.Lstat(vol.Mountpoint)
	if err != nil {
		return 0, fmt.Errorf("agent: docker backup: stat mountpoint %s: %w", vol.Mountpoint, err)
	}
	if !info.IsDir() {
		if err := addFSEntry(tw, vol.Mountpoint, info, filepath.Base(vol.Mountpoint), fsOpts, &st); err != nil {
			return st.bytesRead, err
		}
		return st.bytesRead, nil
	}
	err = filepath.WalkDir(vol.Mountpoint, func(path string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			return fmt.Errorf("agent: docker backup: walk %s: %w", path, err)
		}
		rel, err := filepath.Rel(vol.Mountpoint, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		fi, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("agent: docker backup: stat %s: %w", path, err)
		}
		return addFSEntry(tw, path, fi, filepath.ToSlash(rel), fsOpts, &st)
	})
	return st.bytesRead, err
}
