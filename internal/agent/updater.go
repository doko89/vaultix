package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
)

// Update outcome statuses (mirror domain.AgentUpdateApplied/Failed).
const (
	updateApplied = "applied"
	updateFailed  = "failed"
)

// stagedVerifyTimeout bounds the staged-binary smoke run.
const stagedVerifyTimeout = 30 * time.Second

// maxReportErrorChars caps failure detail (the manager truncates at 4KiB;
// stay well under so reports are never rejected).
const maxReportErrorChars = 2000

// Updater applies manager-ordered self-updates: download, verify, swap,
// restart. It only acts when idle — the directive re-advertises every
// poll, so a busy agent simply defers.
type Updater struct {
	Client  ManagerClient
	Version string // current binary version
	Logger  *slog.Logger
	// ExecPath overrides os.Executable (tests stage into temp dirs).
	ExecPath string
	// Exec replaces the process image; defaults to procSelfExec.
	Exec func(argv0 string, argv, env []string) error
	// RunVersion smoke-tests staged bytes (`bin version`, exit 0);
	// defaults to stagedVersionRun. A seam for deterministic tests.
	RunVersion func(ctx context.Context, bin string) error
}

func (u *Updater) logger() *slog.Logger {
	if u.Logger != nil {
		return u.Logger
	}
	return slog.Default()
}

func (u *Updater) execPath() (string, error) {
	if u.ExecPath != "" {
		return u.ExecPath, nil
	}
	return os.Executable()
}

// Apply carries out dir when it outranks the running version and idle()
// reports no in-flight backup or restore. A nil directive, a stale
// target, or a busy agent is a quiet no-op (nil). Failures report
// failed to the manager and return the error for logging.
func (u *Updater) Apply(ctx context.Context, dir *UpdateDirective, idle func() bool) error {
	if dir == nil {
		return nil
	}
	if strings.TrimSpace(dir.Version) == "" {
		return fmt.Errorf("agent: update directive without version")
	}
	if domain.CompareVersions(u.Version, dir.Version) >= 0 {
		return nil
	}
	if idle != nil && !idle() {
		u.logger().Debug("agent update deferred: job in flight", "target", dir.Version)
		return nil
	}
	if err := u.apply(ctx, dir); err != nil {
		u.report(ctx, dir.Version, updateFailed, err.Error())
		return err
	}
	return nil
}

func (u *Updater) apply(ctx context.Context, dir *UpdateDirective) error {
	exe, err := u.execPath()
	if err != nil {
		return fmt.Errorf("agent: update: resolve executable: %w", err)
	}
	pkg, err := u.Client.DownloadUpdate(ctx)
	if err != nil {
		return fmt.Errorf("agent: update: download: %w", err)
	}
	if err := verifyPackage(pkg, dir); err != nil {
		return err
	}
	staged := exe + ".new"
	backup := exe + ".prev"
	if err := os.WriteFile(staged, pkg.Bytes, 0o755); err != nil {
		return fmt.Errorf("agent: update: stage binary: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(staged)
		}
	}()
	runVersion := u.RunVersion
	if runVersion == nil {
		runVersion = stagedVersionRun
	}
	vctx, cancel := context.WithTimeout(ctx, stagedVerifyTimeout)
	defer cancel()
	if err := runVersion(vctx, staged); err != nil {
		return fmt.Errorf("agent: update: staged binary failed smoke run: %w", err)
	}
	_ = os.Remove(backup)
	if err := os.Rename(exe, backup); err != nil {
		return fmt.Errorf("agent: update: back up current binary: %w", err)
	}
	if err := os.Rename(staged, exe); err != nil {
		// Best-effort restore; the process still runs the old image.
		_ = os.Rename(backup, exe)
		return fmt.Errorf("agent: update: swap binary: %w", err)
	}
	cleanup = false
	u.report(ctx, dir.Version, updateApplied, "")
	u.logger().Info("agent updated, restarting", "version", dir.Version)
	execFn := u.Exec
	if execFn == nil {
		execFn = procSelfExec
	}
	if err := execFn(exe, os.Args, os.Environ()); err != nil {
		// Swap succeeded but the process image did not: restore the
		// backup so a supervisor restart boots the known-good binary,
		// and report failed.
		_ = os.Remove(exe)
		_ = os.Rename(backup, exe)
		u.report(ctx, dir.Version, updateFailed, fmt.Sprintf("restart into new binary: %v", err))
		return fmt.Errorf("agent: update: restart: %w", err)
	}
	return nil
}

// verifyPackage checks the download against the directive (authority),
// not the transport headers: size first, then sha256.
func verifyPackage(pkg UpdatePackage, dir *UpdateDirective) error {
	if strings.TrimSpace(pkg.Version) != "" && pkg.Version != dir.Version {
		return fmt.Errorf("agent: update: download is %q, directive wants %q", pkg.Version, dir.Version)
	}
	if int64(len(pkg.Bytes)) != dir.SizeBytes {
		return fmt.Errorf("agent: update: size %d, directive wants %d", len(pkg.Bytes), dir.SizeBytes)
	}
	want, err := hex.DecodeString(strings.TrimSpace(dir.SHA256))
	if err != nil || len(want) == 0 {
		return fmt.Errorf("agent: update: directive carries invalid sha256")
	}
	sum := sha256.Sum256(pkg.Bytes)
	if !bytes.Equal(sum[:], want) {
		return fmt.Errorf("agent: update: sha256 mismatch (bytes rejected)")
	}
	return nil
}

// stagedVersionRun smoke-tests staged bytes: `bin version` must exit 0.
func stagedVersionRun(ctx context.Context, bin string) error {
	cmd := exec.CommandContext(ctx, bin, "version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("exit: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (u *Updater) report(ctx context.Context, version, status, errMsg string) {
	if len(errMsg) > maxReportErrorChars {
		errMsg = errMsg[:maxReportErrorChars]
	}
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := u.Client.ReportUpdate(rctx, UpdateReportRequest{Status: status, Version: version, Error: errMsg}); err != nil {
		u.logger().Warn("agent update report failed", "status", status, "error", err)
	}
}
