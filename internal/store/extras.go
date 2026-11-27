package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LastModel reads the resolved model preference from the conversation namespace.
func (s Store) LastModel() string { return s.preference("last-model", 2049, validModel) }

// Language reads the UI language from the shared state directory.
func (s Store) Language() string { return s.preference("ui-language", 3, validLanguage) }

func (s Store) preference(name string, max int64, valid func(string) bool) string {
	dir, err := s.openRoot(false)
	if err != nil {
		return ""
	}
	defer dir.Close()
	b, err := readSmallFile(dir, name, max)
	if err != nil {
		return ""
	}
	value := strings.TrimSuffix(string(b), "\n")
	if !valid(value) {
		return ""
	}
	return value
}

func (s Store) SaveModel(id string) error { return s.savePreference("last-model", id, validModel) }
func (s Store) SaveLanguage(code string) error {
	return s.savePreference("ui-language", code, validLanguage)
}

func (s Store) savePreference(name, value string, valid func(string) bool) error {
	if !s.Enabled {
		return nil
	}
	if !valid(value) {
		return fmt.Errorf("invalid %s preference", name)
	}
	dir, err := s.openRoot(true)
	if err != nil {
		return err
	}
	defer dir.Close()
	return atomicWrite(dir, name, []byte(value+"\n"))
}

// Attachment returns a checked path for an explicitly requested saved image.
// Indexes are 1-based. Like any returned pathname, it is not a pinned handle;
// callers should use it immediately and only with a trusted history root.
func (s Store) Attachment(session Session, turn, image int) (string, error) {
	if !validID(session.ID) {
		return "", errors.New("invalid session ID")
	}
	if turn < 1 || turn > len(session.Turns) {
		return "", errors.New("turn index out of range")
	}
	attachments := session.Turns[turn-1].Attachments
	if image < 1 || image > len(attachments) {
		return "", errors.New("attachment index out of range")
	}
	relative := attachments[image-1].File
	if relative == "" {
		return "", errors.New("attachment was not saved (save_images was disabled)")
	}
	if !attachmentRE.MatchString(relative) {
		return "", errors.New("invalid attachment path")
	}
	root, err := s.openRoot(false)
	if err != nil {
		return "", err
	}
	defer root.Close()
	err = withSession(root, session.ID, false, false, func(dir *os.File) error {
		ad, err := childDirectory(dir, "attachments", false)
		if err != nil {
			return err
		}
		defer ad.Close()
		return regularAt(ad, strings.TrimPrefix(relative, "attachments/"))
	})
	if err != nil {
		return "", err
	}
	return filepath.Join(s.Root, session.ID, relative), nil
}

// Export is an explicit opt-in write even with history disabled. It will not
// overwrite any existing entry, create a parent directory, or copy images.
func (s Store) Export(session Session, path string) error {
	if err := validateSession(&session); err != nil {
		return err
	}
	if path == "" || strings.HasSuffix(path, string(os.PathSeparator)) {
		return errors.New("export requires a file path")
	}
	dir, err := openDirectory(filepath.Dir(path), false)
	if err != nil {
		return err
	}
	defer dir.Close()
	state := diskState{}
	if s.Enabled {
		if root, err := s.openRoot(false); err == nil {
			_ = withSession(root, session.ID, false, false, func(dir *os.File) error {
				var err error
				state, err = readState(dir, session.ID)
				return err
			})
			root.Close()
		}
	}
	return writeExclusive(dir, filepath.Base(path), []byte(markdown(session, state)))
}

func codeSpan(text string) string {
	fence := "`"
	for strings.Contains(text, fence) {
		fence += "`"
	}
	if strings.HasPrefix(text, "`") || strings.HasSuffix(text, "`") {
		text = " " + text + " "
	}
	return fence + text + fence
}

func markdown(s Session, state diskState) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n- Session ID: %s\n", s.Name, codeSpan(s.ID))
	if s.Model != "" {
		fmt.Fprintf(&b, "- Model: %s\n", codeSpan(s.Model))
	}
	if state.Created == "" && len(s.Turns) > 0 {
		state.Created = s.Turns[0].Timestamp
	}
	if state.Created != "" {
		fmt.Fprintf(&b, "- Created: %s\n", state.Created)
	}
	if state.Updated != "" {
		fmt.Fprintf(&b, "- Updated: %s\n", state.Updated)
	}
	fmt.Fprintf(&b, "- Context start: %d\n\n", s.ContextStart)
	completed := 0
	var input, output uint64
	for i, t := range s.Turns {
		if t.Status == complete {
			completed++
		}
		fmt.Fprintf(&b, "## Turn %d (%s)\n\nModel: %s\n\nTime: %s\n\n### User\n\n%s\n\n### Assistant\n\n%s\n\n", i+1, t.Status, codeSpan(t.Model), t.Timestamp, t.User, t.Assistant)
		if len(t.Attachments) > 0 {
			b.WriteString("### Attachments\n\n")
			for _, a := range t.Attachments {
				location := "not saved"
				if a.File != "" {
					location = codeSpan(a.File)
				}
				fmt.Fprintf(&b, "- %s (%s %d×%d, %d bytes) — %s\n", codeSpan(a.Name), a.Format, a.Width, a.Height, a.Size, location)
			}
			b.WriteByte('\n')
		}
		if u := t.Usage; u != nil {
			if u.InputTokens > 0 {
				input += uint64(u.InputTokens)
			}
			if u.OutputTokens > 0 {
				output += uint64(u.OutputTokens)
			}
			fmt.Fprintf(&b, "Usage: input_tokens=%d, output_tokens=%d, total_tokens=%d\n\n", u.InputTokens, u.OutputTokens, u.TotalTokens)
		} else {
			b.WriteString("Usage: unavailable\n\n")
		}
	}
	fmt.Fprintf(&b, "## Summary\n\n- Complete: %d\n- Incomplete/failed: %d\n- Input tokens: %d; output tokens: %d (reported usage only)\n", completed, len(s.Turns)-completed, input, output)
	return b.String()
}
