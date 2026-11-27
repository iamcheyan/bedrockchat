package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const preferencesName = "runtime-preferences.json"
const maxPreferencesBytes = 16 * 1024

// RuntimePreferences contains validated runtime settings safe to persist in
// the shared state directory. Model and UI language are stored elsewhere.
type RuntimePreferences struct {
	Provider        string `json:"provider,omitempty"`
	Endpoint        string `json:"endpoint,omitempty"`
	Region          string `json:"region,omitempty"`
	Profile         string `json:"profile,omitempty"`
	Thinking        string `json:"thinking,omitempty"`
	ResponseTimeout string `json:"response_timeout,omitempty"`
	MaxInput        int    `json:"max_input,omitempty"`
	MaxOutput       int    `json:"max_output,omitempty"`
	SaveImages      *bool  `json:"save_images,omitempty"`
	Stream          *bool  `json:"stream,omitempty"`
	MaxEdge         int    `json:"max_edge,omitempty"`
	MaxBytes        int    `json:"max_bytes,omitempty"`
}

var profileRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// RuntimePreferences loads the validated shared runtime preferences. It does
// not create the state directory or lock file when no preferences exist.
func (s Store) RuntimePreferences() (RuntimePreferences, error) {
	if !s.Enabled {
		return RuntimePreferences{}, nil
	}
	dir, err := s.openRoot(false)
	if errors.Is(err, os.ErrNotExist) {
		return RuntimePreferences{}, nil
	}
	if err != nil {
		return RuntimePreferences{}, errors.New("cannot read runtime preferences")
	}
	defer dir.Close()
	lock, err := lockPreferences(dir, false)
	if err != nil {
		return RuntimePreferences{}, errors.New("cannot read runtime preferences")
	}
	if lock != nil {
		defer unlockPreferences(lock)
	}
	prefs, err := readPreferences(dir)
	if err != nil {
		return RuntimePreferences{}, errors.New("invalid runtime preferences")
	}
	return prefs, nil
}

// UpdateRuntimePreferences applies and atomically persists a validated update.
// The exclusive flock covers reading, callback mutation, validation and rename.
func (s Store) UpdateRuntimePreferences(update func(*RuntimePreferences) error) error {
	if !s.Enabled {
		return nil
	}
	if update == nil {
		return errors.New("invalid runtime preferences update")
	}
	dir, err := s.openRoot(true)
	if err != nil {
		return errors.New("cannot update runtime preferences")
	}
	defer dir.Close()
	lock, err := lockPreferences(dir, true)
	if err != nil {
		return errors.New("cannot update runtime preferences")
	}
	defer unlockPreferences(lock)
	prefs, err := readPreferences(dir)
	if err != nil {
		return errors.New("invalid runtime preferences")
	}
	if err := update(&prefs); err != nil {
		return errors.New("invalid runtime preferences update")
	}
	if err := validatePreferences(prefs); err != nil {
		return errors.New("invalid runtime preferences")
	}
	data, err := json.Marshal(prefs)
	if err != nil || len(data) > maxPreferencesBytes {
		return errors.New("invalid runtime preferences")
	}
	if err := atomicWrite(dir, preferencesName, append(data, '\n')); err != nil {
		return errors.New("cannot update runtime preferences")
	}
	return nil
}

func lockPreferences(dir *os.File, write bool) (*os.File, error) {
	flags, operation := unix.O_RDONLY, unix.LOCK_SH
	if write {
		flags, operation = unix.O_RDWR|unix.O_CREAT, unix.LOCK_EX
	}
	f, err := openRegular(dir, ".preferences.lock", flags)
	if !write && errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if write {
		if err := f.Chmod(0600); err != nil {
			f.Close()
			return nil, err
		}
	}
	for {
		err = unix.Flock(int(f.Fd()), operation)
		if !errors.Is(err, unix.EINTR) {
			break
		}
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func unlockPreferences(f *os.File) {
	_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
	_ = f.Close()
}

func readPreferences(dir *os.File) (RuntimePreferences, error) {
	f, err := openRegular(dir, preferencesName, unix.O_RDONLY)
	if errors.Is(err, os.ErrNotExist) {
		return RuntimePreferences{}, nil
	}
	if err != nil {
		return RuntimePreferences{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxPreferencesBytes+1))
	if err != nil || len(data) > maxPreferencesBytes {
		return RuntimePreferences{}, errors.New("invalid data")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var prefs RuntimePreferences
	if err := dec.Decode(&prefs); err != nil {
		return RuntimePreferences{}, errors.New("invalid data")
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return RuntimePreferences{}, errors.New("invalid data")
	}
	if err := validatePreferences(prefs); err != nil {
		return RuntimePreferences{}, err
	}
	return prefs, nil
}

func validatePreferences(p RuntimePreferences) error {
	if p.Provider != "" && p.Provider != "bedrock" && p.Provider != "openai" {
		return errors.New("invalid provider")
	}
	if p.Endpoint != "" {
		canonical, err := canonicalEndpoint(p.Endpoint)
		if err != nil || canonical != p.Endpoint {
			return errors.New("invalid endpoint")
		}
	}
	if p.Region != "" && !regionRE.MatchString(p.Region) {
		return errors.New("invalid region")
	}
	if p.Profile != "" && !profileRE.MatchString(p.Profile) {
		return errors.New("invalid profile")
	}
	if p.Thinking != "" && p.Thinking != "auto" && p.Thinking != "on" && p.Thinking != "off" {
		return errors.New("invalid thinking mode")
	}
	if p.ResponseTimeout != "" {
		d, err := time.ParseDuration(p.ResponseTimeout)
		if err != nil || d < time.Second || d > time.Hour {
			return errors.New("invalid response timeout")
		}
	}
	if p.MaxInput != 0 && (p.MaxInput < 64 || p.MaxInput > 4000000) ||
		p.MaxOutput != 0 && (p.MaxOutput < 1 || p.MaxOutput > 256000) ||
		p.MaxEdge != 0 && (p.MaxEdge < 64 || p.MaxEdge > 8000) ||
		p.MaxBytes != 0 && (p.MaxBytes < 1024 || p.MaxBytes > 3750000) {
		return errors.New("invalid numeric preference")
	}
	if strings.ContainsAny(p.Profile, "\x00\n\r") {
		return errors.New("invalid profile")
	}
	return nil
}
