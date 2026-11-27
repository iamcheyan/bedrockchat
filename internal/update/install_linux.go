package update

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

func runPlatform(ctx context.Context, current string, out io.Writer) error {
	client := newClient()
	defer client.CloseIdleConnections()
	return run(ctx, current, out, client, func() (string, os.FileInfo, error) {
		// On Linux, /proc/self/exe identifies the running inode even if another
		// process replaces the pathname while this update is in progress.
		running, err := os.Stat("/proc/self/exe")
		if err != nil {
			return "", nil, err
		}
		path, err := os.Executable()
		return path, running, err
	})
}

func run(ctx context.Context, current string, out io.Writer, client *http.Client, executable func() (string, os.FileInfo, error)) error {
	var version []string
	if current != "dev" && current != "(devel)" {
		var err error
		version, err = parseVersion(current)
		if err != nil {
			return fmt.Errorf("current version: %w", err)
		}
	}
	rel, err := latest(ctx, client)
	if err != nil {
		return fmt.Errorf("check latest release: %w", err)
	}
	candidate, err := parseVersion(rel.Tag)
	if err != nil || candidate[4] != "" || rel.Draft || rel.Prerelease {
		return fmt.Errorf("latest release is not a stable semantic version: %q", rel.Tag)
	}
	if !isNewer(candidate, version) {
		status(out, "Already up to date (%s; latest stable: %s).\n", current, rel.Tag)
		return nil
	}
	binary, err := rel.findAsset("bedrockchat-linux-"+runtime.GOARCH, minBinarySize, maxBinarySize)
	if err != nil {
		return err
	}
	checksums, err := rel.findAsset(checksumsName, 1, maxSumsSize)
	if err != nil {
		return err
	}
	var sums bytes.Buffer
	if _, err := checksums.download(ctx, client, &sums); err != nil {
		return fmt.Errorf("download %s: %w", checksumsName, err)
	}
	want, err := publishedChecksum(&sums, binary.Name)
	if err != nil {
		return err
	}
	if binary.Digest != "" && !strings.EqualFold(binary.Digest, "sha256:"+want) {
		return errors.New("GitHub binary digest disagrees with SHA256SUMS")
	}
	path, running, err := executable()
	if err != nil {
		return fmt.Errorf("locate running executable: %w", err)
	}
	if err := install(ctx, path, running, binary, want, client); err != nil {
		return fmt.Errorf("install %s: %w", rel.Tag, err)
	}
	status(out, "Updated bedrockchat from %s to %s.\n", current, rel.Tag)
	return nil
}

func status(out io.Writer, format string, args ...any) {
	if out != nil {
		fmt.Fprintf(out, format, args...)
	}
}

func checkTarget(info, running os.FileInfo) error {
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 || !os.SameFile(info, running) {
		return errors.New("target is not the running regular executable")
	}
	if info.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 ||
		os.Getuid() != os.Geteuid() || os.Getgid() != os.Getegid() ||
		info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return errors.New("refusing to replace a set-ID executable or one owned by another user")
	}
	if info.Mode().Perm()&0022 != 0 {
		return errors.New("refusing to replace a group- or world-writable executable")
	}
	return nil
}

func install(ctx context.Context, path string, running os.FileInfo, binary asset, want string, client *http.Client) error {
	if !filepath.IsAbs(path) {
		return errors.New("executable path is not absolute")
	}
	dir, base := filepath.Dir(path), filepath.Base(path)
	// Anchor staging, cleanup and rename to the same directory inode.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	parent, err := root.Stat(".")
	if err != nil {
		return err
	}
	if parent.Mode().Perm()&0022 != 0 {
		return errors.New("refusing to stage an update in a group- or world-writable directory")
	}
	before, err := root.Lstat(base)
	if err != nil {
		return err
	}
	if err := checkTarget(before, running); err != nil {
		return err
	}

	stage := ".bedrockchat-update-" + rand.Text()
	f, err := root.OpenFile(stage, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(stage)
	defer f.Close()

	actual, err := binary.download(ctx, client, f)
	if err != nil {
		return fmt.Errorf("download %s: %w", binary.Name, err)
	}
	if actual != want {
		return errors.New("binary SHA-256 does not match SHA256SUMS")
	}
	if err := checkELF(f, uint64(binary.Size), runtime.GOARCH); err != nil {
		return err
	}
	initialStage, err := f.Stat()
	if err != nil {
		return err
	}
	originalGroup := before.Sys().(*syscall.Stat_t).Gid
	if initialStage.Sys().(*syscall.Stat_t).Gid != originalGroup {
		if err := f.Chown(-1, int(originalGroup)); err != nil {
			return fmt.Errorf("preserve executable group: %w", err)
		}
	}
	// Only ordinary permission bits, and only those already on the executable.
	if err := f.Chmod(before.Mode().Perm()); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	staged, err := f.Stat()
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	// Serialize the final check/rename among updater instances without leaving
	// a lock file behind. A stale instance will then fail the inode check.
	lock, err := root.Open(".")
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("lock executable directory: %w", err)
	}
	after, err := root.Lstat(base)
	if err != nil {
		return err
	}
	if err := checkTarget(after, running); err != nil {
		return err
	}
	if after.Mode() != before.Mode() || after.Sys().(*syscall.Stat_t).Gid != originalGroup {
		return errors.New("executable permissions changed during update")
	}
	atPath, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !os.SameFile(atPath, after) {
		return errors.New("executable path changed during update")
	}
	atStage, err := root.Lstat(stage)
	if err != nil {
		return err
	}
	if !os.SameFile(atStage, staged) || atStage.Mode() != staged.Mode() {
		return errors.New("staged executable changed during update")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Rename is the commit point; never return an error after it succeeds.
	return root.Rename(stage, base)
}
