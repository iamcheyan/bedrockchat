package store

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/iamcheyan/bedrockchat/internal/chat"
	"golang.org/x/sys/unix"
	"golang.org/x/text/cases"
)

// Store uses Root as the conversation directory (or the shared state directory
// for UI preferences). Each writer needs its own in-memory Session.
type Store struct {
	Root                string
	Enabled, SaveImages bool
}

type Session struct {
	ID, Name, Model string
	ContextStart    int
	Turns           []Turn
}

type Turn struct {
	Model, User, Assistant, Status string
	Usage                          *chat.Usage
	Attachments                    []Attachment
	Timestamp                      string
}

type Attachment struct {
	Name, File, Format  string
	Width, Height, Size int
}

const defaultName = "未命名"
const complete = "complete"

var (
	prefixRE     = regexp.MustCompile(`^[0-9a-f]{1,32}$`)
	formatRE     = regexp.MustCompile(`^[A-Za-z0-9]{1,16}$`)
	statusRE     = regexp.MustCompile(`^[a-z_]{1,32}$`)
	attachmentRE = regexp.MustCompile(`^attachments/[0-9]{4,}-[0-9]{2,}\.[a-z0-9]{1,16}$`)
)

func now() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000000+00:00") }

// New creates an in-memory session without touching disk. An empty name gets
// the legacy default. State and SaveTurn validate explicit names on save.
func (s Store) New(name, model string) Session {
	var id [16]byte
	rand.Read(id[:])
	id[6] = (id[6] & 0x0f) | 0x40 // UUID version 4, legacy hex representation
	id[8] = (id[8] & 0x3f) | 0x80
	if name == "" {
		name = defaultName
	}
	return Session{ID: hex.EncodeToString(id[:]), Name: name, Model: model}
}

func validID(s string) bool { return len(s) == 32 && prefixRE.MatchString(s) }
func singleLine(s string) bool {
	return utf8.ValidString(s) && strings.IndexFunc(s, unicode.IsControl) < 0
}
func validName(s string) bool {
	return singleLine(s) && strings.TrimSpace(s) != "" && utf8.RuneCountInString(s) <= 200
}

func validateSession(s *Session) error {
	if s == nil || !validID(s.ID) {
		return errors.New("session ID must be 32 lowercase hex digits")
	}
	if !validName(s.Name) {
		return errors.New("session name must contain 1–200 characters and no control characters")
	}
	if s.ContextStart < 0 {
		return errors.New("context start cannot be negative")
	}
	if !singleLine(s.Model) {
		return errors.New("invalid session model")
	}
	return nil
}

func validateTurn(t Turn) error {
	if t.Model == "" || !singleLine(t.Model) || t.Timestamp == "" || !singleLine(t.Timestamp) || !statusRE.MatchString(t.Status) {
		return errors.New("invalid turn model, timestamp, or status")
	}
	if !utf8.ValidString(t.User) || !utf8.ValidString(t.Assistant) {
		return errors.New("turn text must be valid UTF-8")
	}
	if u := t.Usage; u != nil && (u.InputTokens < 0 || u.OutputTokens < 0 || u.TotalTokens < 0) {
		return errors.New("token usage cannot be negative")
	}
	for _, a := range t.Attachments {
		if !singleLine(a.Name) || strings.TrimSpace(a.Name) == "" || strings.ContainsAny(a.Name, `/\`) ||
			!formatRE.MatchString(a.Format) || a.Width < 0 || a.Height < 0 || a.Size < 0 ||
			a.File != "" && !attachmentRE.MatchString(a.File) {
			return errors.New("invalid attachment metadata")
		}
	}
	return nil
}

// withSession keeps directories pinned while obtaining the same lock Python
// uses. Once locked, ensure the entry still names the opened directory: a
// concurrent delete may have removed it while we were waiting for the lock.
func withSession(root *os.File, id string, create, write bool, action func(*os.File) error) error {
	dir, err := childDirectory(root, id, create)
	if err != nil {
		return err
	}
	defer dir.Close()
	if write {
		if err := privateDirectory(dir); err != nil {
			return err
		}
	}
	lock, err := lockSession(dir, write)
	if err != nil {
		return err
	}
	if lock != nil {
		defer lock.Close()
	}
	var opened, current unix.Stat_t
	if err := unix.Fstat(int(dir.Fd()), &opened); err != nil {
		return err
	}
	if err := unix.Fstatat(int(root.Fd()), id, &current, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if opened.Dev != current.Dev || opened.Ino != current.Ino {
		return errors.New("session directory changed while opening it")
	}
	return action(dir)
}

// State persists the name, model and context start, without dropping any turns.
func (s Store) State(session *Session) error {
	if err := validateSession(session); err != nil {
		return err
	}
	if !s.Enabled {
		return nil
	}
	root, err := s.openRoot(true)
	if err != nil {
		return err
	}
	defer root.Close()
	return withSession(root, session.ID, true, true, func(dir *os.File) error {
		state, err := readState(dir, session.ID)
		if err != nil {
			return err
		}
		state.Name, state.Model, state.ContextStart = session.Name, session.Model, &session.ContextStart
		return writeState(dir, state)
	})
}

func prepareTurn(session *Session, turn Turn, images []chat.Image, saveImages bool) (Turn, error) {
	if turn.Model == "" {
		turn.Model = session.Model
	}
	if turn.Status == "" {
		turn.Status = complete
	}
	if turn.Timestamp == "" {
		turn.Timestamp = now()
	}
	turn.Attachments = slices.Clone(turn.Attachments)
	if len(turn.Attachments) == 0 && len(images) != 0 {
		for _, im := range images {
			turn.Attachments = append(turn.Attachments, Attachment{Name: im.Name, Format: im.Format, Width: im.Width, Height: im.Height, Size: len(im.Data)})
		}
	}
	if turn.Usage != nil {
		usage := *turn.Usage
		turn.Usage = &usage
	}
	if err := validateTurn(turn); err != nil {
		return Turn{}, err
	}
	// Never accept a caller-supplied path as evidence that bytes were persisted.
	for i := range turn.Attachments {
		turn.Attachments[i].File = ""
	}
	if saveImages && len(images) != len(turn.Attachments) {
		return Turn{}, errors.New("image count does not match attachment metadata")
	}
	return turn, nil
}

// SaveTurn appends a JSONL record. With no history it only updates session.
// Images supply metadata if turn.Attachments is empty; otherwise the supplied
// metadata is retained. Image bytes are written only when SaveImages is set.
func (s Store) SaveTurn(session *Session, turn Turn, images []chat.Image) error {
	if err := validateSession(session); err != nil {
		return err
	}
	turn, err := prepareTurn(session, turn, images, s.Enabled && s.SaveImages)
	if err != nil {
		return err
	}
	if !s.Enabled {
		session.Turns = append(session.Turns, turn)
		return nil
	}
	root, err := s.openRoot(true)
	if err != nil {
		return err
	}
	defer root.Close()
	duplicate := false
	err = withSession(root, session.ID, true, true, func(dir *os.File) error {
		state, err := readState(dir, session.ID)
		if err != nil {
			return err
		}
		records, err := readTurns(dir)
		if err != nil {
			return err
		}
		if state.Name == "" {
			state.Name, state.Model, state.ContextStart = session.Name, session.Model, &session.ContextStart
		}
		if state.Created == "" && len(records) > 0 {
			state.Created = records[0].Timestamp
		}
		if n := len(records); n > 0 && sameTurn(records[n-1], turn) {
			turn = records[n-1]
			duplicate = true
			return writeState(dir, state)
		}
		return persistTurn(dir, state, turn, images, s.SaveImages, len(records)+1)
	})
	if err != nil {
		return err
	}
	if !duplicate || len(session.Turns) == 0 || !sameTurn(session.Turns[len(session.Turns)-1], turn) {
		session.Turns = append(session.Turns, turn)
	}
	return nil
}

func sameTurn(a, b Turn) bool {
	aa, ba := a.Attachments, b.Attachments
	a.Attachments, b.Attachments = nil, nil
	if !reflect.DeepEqual(a, b) {
		return false
	}
	return slices.EqualFunc(aa, ba, func(x, y Attachment) bool { x.File, y.File = "", ""; return x == y })
}

func sessionIDs(root *os.File) ([]string, error) {
	entries, err := root.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if validID(e.Name()) && e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	return ids, nil
}

func resolve(root *os.File, prefix string) (string, error) {
	ids, err := sessionIDs(root)
	if err != nil {
		return "", err
	}
	id := ""
	for _, candidate := range ids {
		if strings.HasPrefix(candidate, prefix) {
			if id != "" {
				return "", errors.New("ambiguous session prefix; use more characters")
			}
			id = candidate
		}
	}
	if id == "" {
		return "", fmt.Errorf("session not found: %w", os.ErrNotExist)
	}
	return id, nil
}

func loadSession(root *os.File, id string) (Session, diskState, error) {
	var sess Session
	var state diskState
	err := withSession(root, id, false, false, func(dir *os.File) error {
		var err error
		state, err = readState(dir, id)
		if err != nil {
			return err
		}
		turns, err := readTurns(dir)
		if err != nil {
			return err
		}
		if state.Name == "" {
			state.Name = defaultName
		}
		if state.Created == "" && len(turns) > 0 {
			state.Created = turns[0].Timestamp
		}
		if state.Updated == "" {
			state.Updated = state.Created
		}
		sess = Session{ID: id, Name: state.Name, Model: state.Model, ContextStart: *state.ContextStart, Turns: turns}
		return nil
	})
	return sess, state, err
}

// Load finds a session by full ID or unambiguous lowercase hex prefix.
func (s Store) Load(prefix string) (Session, error) {
	if !prefixRE.MatchString(prefix) {
		return Session{}, errors.New("invalid session ID or prefix")
	}
	root, err := s.openRoot(false)
	if err != nil {
		return Session{}, err
	}
	defer root.Close()
	id, err := resolve(root, prefix)
	if err != nil {
		return Session{}, err
	}
	sess, _, err := loadSession(root, id)
	return sess, err
}

// List returns newest-first sessions, skipping broken entries. A blank query
// omits turns. A nonblank query includes the full transcript for each match.
func (s Store) List(query string) ([]Session, error) {
	root, err := s.openRoot(false)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer root.Close()
	ids, err := sessionIDs(root)
	if err != nil {
		return nil, err
	}
	type item struct {
		session Session
		updated string
	}
	var found []item
	fold := cases.Fold()
	needle := fold.String(query)
	match := func(value string) bool { return strings.Contains(fold.String(value), needle) }
	for _, id := range ids {
		sess, state, err := loadSession(root, id)
		if err != nil {
			continue
		}
		if needle == "" {
			sess.Turns = nil
		} else if !sessionMatches(sess, match) {
			continue
		}
		found = append(found, item{sess, state.Updated})
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].updated == found[j].updated {
			return found[i].session.ID < found[j].session.ID
		}
		return found[i].updated > found[j].updated
	})
	sessions := make([]Session, 0, len(found))
	for _, v := range found {
		sessions = append(sessions, v.session)
	}
	return sessions, nil
}

func sessionMatches(s Session, match func(string) bool) bool {
	if match(s.Name) || match(s.Model) {
		return true
	}
	for _, t := range s.Turns {
		if match(t.User) || match(t.Assistant) {
			return true
		}
		for _, a := range t.Attachments {
			if match(a.Name) {
				return true
			}
		}
	}
	return false
}

// Delete removes a session, never following links within it.
func (s Store) Delete(prefix string) error {
	if !prefixRE.MatchString(prefix) {
		return errors.New("invalid session ID or prefix")
	}
	root, err := s.openRoot(false)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := privateDirectory(root); err != nil {
		return err
	}
	id, err := resolve(root, prefix)
	if err != nil {
		return err
	}
	return withSession(root, id, false, true, func(dir *os.File) error {
		tomb := ".deleted-" + rand.Text()
		if err := unix.Renameat(int(root.Fd()), id, int(root.Fd()), tomb); err != nil {
			return err
		}
		err := removeTree(root, tomb)
		_ = root.Sync()
		return err
	})
}
