package store

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// All history IO uses directory descriptors and no-follow opens, including
// ancestors of the root. Caller-selected config files are handled separately.
const directoryFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC | unix.O_NOFOLLOW

func openDirectory(path string, create bool) (*os.File, error) {
	if path == "" {
		return nil, errors.New("missing directory path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	fd, err := unix.Open("/", directoryFlags, 0)
	if err != nil {
		return nil, err
	}
	dir := os.NewFile(uintptr(fd), "/")
	for name := range strings.SplitSeq(strings.TrimPrefix(abs, "/"), "/") {
		if name == "" {
			continue
		}
		next, err := childDirectory(dir, name, create)
		dir.Close()
		if err != nil {
			return nil, err
		}
		dir = next
	}
	return dir, nil
}

func childDirectory(parent *os.File, name string, create bool) (*os.File, error) {
	fd, err := unix.Openat(int(parent.Fd()), name, directoryFlags, 0)
	if create && errors.Is(err, os.ErrNotExist) {
		if err := unix.Mkdirat(int(parent.Fd()), name, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		fd, err = unix.Openat(int(parent.Fd()), name, directoryFlags, 0)
	}
	if err != nil {
		return nil, fmt.Errorf("open directory %s: %w", name, err)
	}
	return os.NewFile(uintptr(fd), name), nil
}

func privateDirectory(dir *os.File) error {
	info, err := dir.Stat()
	if err != nil {
		return err
	}
	if info.Mode().Perm() == 0700 && info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) == 0 {
		return nil
	}
	return dir.Chmod(0700)
}

func (s Store) requireDedicatedRoot() error {
	abs, err := filepath.Abs(s.Root)
	if err != nil {
		return err
	}
	clean := filepath.Clean(s.Root)
	if s.Root == "" || abs == "/" || clean == "." || clean == ".." {
		return errors.New("history requires a dedicated directory")
	}
	return nil
}

func (s Store) openRoot(write bool) (*os.File, error) {
	if !s.Enabled {
		return nil, errors.New("history is disabled")
	}
	if err := s.requireDedicatedRoot(); err != nil {
		return nil, err
	}
	dir, err := openDirectory(s.Root, write)
	if err != nil {
		return nil, err
	}
	if write {
		if err := privateDirectory(dir); err != nil {
			dir.Close()
			return nil, err
		}
	}
	return dir, nil
}

func openRegular(dir *os.File, name string, flags int) (*os.File, error) {
	fd, err := unix.Openat(int(dir.Fd()), name, flags|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", name, err)
	}
	f := os.NewFile(uintptr(fd), name)
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		f.Close()
		return nil, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 {
		f.Close()
		return nil, fmt.Errorf("%s is not a regular file with a single link", name)
	}
	return f, nil
}

func regularAt(dir *os.File, name string) error {
	var st unix.Stat_t
	if err := unix.Fstatat(int(dir.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 {
		return fmt.Errorf("%s is not a regular file with a single link", name)
	}
	return nil
}

func removeAt(dir *os.File, name string) error { return unix.Unlinkat(int(dir.Fd()), name, 0) }

func writeExclusive(dir *os.File, name string, data []byte) error {
	f, err := openRegular(dir, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL)
	if err != nil {
		return err
	}
	err = f.Chmod(0600)
	if err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err = errors.Join(err, closeErr); err != nil {
		_ = removeAt(dir, name)
	}
	return err
}

func atomicWrite(dir *os.File, name string, data []byte) error {
	if err := regularAt(dir, name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temp := "." + name + "." + rand.Text() + ".tmp"
	if err := writeExclusive(dir, temp, data); err != nil {
		return err
	}
	if err := unix.Renameat(int(dir.Fd()), temp, int(dir.Fd()), name); err != nil {
		_ = removeAt(dir, temp)
		return err
	}
	_ = dir.Sync() // not supported on every filesystem; the contents were fsynced before rename
	return nil
}

func lockSession(dir *os.File, write bool) (*os.File, error) {
	flags, operation := unix.O_RDONLY, unix.LOCK_SH
	if write {
		flags, operation = unix.O_RDWR|unix.O_CREAT, unix.LOCK_EX
	}
	f, err := openRegular(dir, ".lock", flags)
	if !write && errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} // legacy state without a lock
	if err != nil {
		return nil, err
	}
	if write {
		err = f.Chmod(0600)
	}
	if err == nil {
		for {
			err = unix.Flock(int(f.Fd()), operation)
			if !errors.Is(err, unix.EINTR) {
				break
			}
		}
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// Removed sessions are first renamed out of the namespace while locked; the
// recursive cleanup never follows symlinks or needs to reopen an absolute path.
func removeTree(parent *os.File, name string) error {
	dir, err := childDirectory(parent, name, false)
	if err != nil {
		return err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			err = removeTree(dir, e.Name())
		} else {
			err = removeAt(dir, e.Name())
		}
		if err != nil {
			return err
		}
	}
	return unix.Unlinkat(int(parent.Fd()), name, unix.AT_REMOVEDIR)
}
