package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/iamcheyan/bedrockchat/internal/chat"
	"golang.org/x/sys/unix"
)

// The wire types preserve Python's field names, nulls and required fields
// without constraining the public in-memory API to the disk format.
type diskState struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Model        string  `json:"-"`
	ModelID      *string `json:"model_id"`
	ContextStart *int    `json:"context_start"`
	Created      string  `json:"created_at"`
	Updated      string  `json:"updated_at"`
}

type diskAttachment struct {
	Name   string          `json:"name"`
	Format string          `json:"format"`
	Width  *int            `json:"width"`
	Height *int            `json:"height"`
	Size   *int            `json:"size"`
	File   json.RawMessage `json:"file"`
}

type diskUsage struct {
	Input  *int `json:"input_tokens"`
	Output *int `json:"output_tokens"`
	Total  *int `json:"total_tokens"`
}

type diskTurn struct {
	Model       string          `json:"model_id"`
	User        *string         `json:"user"`
	Assistant   *string         `json:"assistant"`
	Status      string          `json:"status"`
	Timestamp   string          `json:"timestamp"`
	Saved       string          `json:"saved_at"`
	Attachments json.RawMessage `json:"attachments"`
	Usage       *diskUsage      `json:"usage"`
}

func decodeJSON(data []byte, dest any) error {
	if !utf8.Valid(data) || !json.Valid(data) {
		return errors.New("invalid JSON")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	return d.Decode(dest)
}

func readSmallFile(dir *os.File, name string, max int64) ([]byte, error) {
	f, err := openRegular(dir, name, unix.O_RDONLY)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err == nil && int64(len(b)) > max {
		return nil, fmt.Errorf("%s exceeds the supported size", name)
	}
	return b, err
}

func readState(dir *os.File, id string) (diskState, error) {
	data, err := readSmallFile(dir, "state.json", 1<<20)
	if errors.Is(err, os.ErrNotExist) {
		return diskState{ID: id, ContextStart: new(int)}, nil
	}
	if err != nil {
		return diskState{}, err
	}
	var state diskState
	if err := decodeJSON(data, &state); err != nil || state.ID != "" && state.ID != id ||
		!validName(state.Name) || state.ContextStart == nil || *state.ContextStart < 0 ||
		state.Created == "" || state.Updated == "" || !singleLine(state.Created) || !singleLine(state.Updated) ||
		state.ModelID != nil && !singleLine(*state.ModelID) {
		return diskState{}, errors.New("invalid or corrupt session state")
	}
	state.ID = id
	if state.ModelID != nil {
		state.Model = *state.ModelID
	}
	return state, nil
}

func writeState(dir *os.File, state diskState) error {
	state.Updated = now()
	if state.Created == "" {
		state.Created = state.Updated
	}
	state.ModelID = nil
	if state.Model != "" {
		state.ModelID = &state.Model
	}
	b, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return atomicWrite(dir, "state.json", b)
}

func decodeTurn(data []byte) (Turn, error) {
	bad := errors.New("invalid turn record")
	var record diskTurn
	if err := decodeJSON(data, &record); err != nil || record.User == nil || record.Assistant == nil || record.Saved == "" {
		return Turn{}, bad
	}
	t := Turn{Model: record.Model, User: *record.User, Assistant: *record.Assistant, Status: record.Status, Timestamp: record.Timestamp}
	if u := record.Usage; u != nil {
		if u.Input == nil || u.Output == nil || u.Total == nil {
			return Turn{}, bad
		}
		t.Usage = &chat.Usage{InputTokens: *u.Input, OutputTokens: *u.Output, TotalTokens: *u.Total}
	}
	if record.Attachments != nil {
		var attachments []diskAttachment
		if err := decodeJSON(record.Attachments, &attachments); err != nil || attachments == nil {
			return Turn{}, bad
		}
		for _, a := range attachments {
			if a.Width == nil || a.Height == nil || a.Size == nil || a.File == nil {
				return Turn{}, bad
			}
			var file *string
			if err := decodeJSON(a.File, &file); err != nil || file != nil && *file == "" {
				return Turn{}, bad
			}
			attachment := Attachment{Name: a.Name, Format: a.Format, Width: *a.Width, Height: *a.Height, Size: *a.Size}
			if file != nil {
				attachment.File = *file
			}
			t.Attachments = append(t.Attachments, attachment)
		}
	}
	if err := validateTurn(t); err != nil {
		return Turn{}, bad
	}
	return t, nil
}

func encodeTurn(t Turn) ([]byte, error) {
	attachments := make([]diskAttachment, 0, len(t.Attachments))
	for _, a := range t.Attachments {
		file := []byte("null")
		if a.File != "" {
			file, _ = json.Marshal(a.File)
		}
		attachments = append(attachments, diskAttachment{Name: a.Name, Format: a.Format, Width: &a.Width, Height: &a.Height, Size: &a.Size, File: file})
	}
	b, err := json.Marshal(attachments)
	if err != nil {
		return nil, err
	}
	r := diskTurn{Model: t.Model, User: &t.User, Assistant: &t.Assistant, Status: t.Status, Timestamp: t.Timestamp, Saved: now(), Attachments: b}
	if u := t.Usage; u != nil {
		r.Usage = &diskUsage{Input: &u.InputTokens, Output: &u.OutputTokens, Total: &u.TotalTokens}
	}
	return json.Marshal(r)
}

func readTurns(dir *os.File) ([]Turn, error) {
	f, err := openRegular(dir, "turns.jsonl", unix.O_RDONLY)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	reader := bufio.NewReader(f)
	var turns []Turn
	for n := 1; ; n++ {
		line, err := reader.ReadBytes('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		if line := bytes.TrimSpace(line); len(line) > 0 {
			t, err := decodeTurn(line)
			if err != nil {
				return nil, fmt.Errorf("corrupt turns.jsonl at line %d: %w", n, err)
			}
			turns = append(turns, t)
		}
		if errors.Is(err, io.EOF) {
			return turns, nil
		}
	}
}

// persistTurn runs under .lock. Keep the old JSONL size until state is committed
// so a failed append/state update doesn't leave a partial record or new images.
func persistTurn(dir *os.File, state diskState, turn Turn, images []chat.Image, saveImages bool, index int) error {
	f, err := openRegular(dir, "turns.jsonl", unix.O_RDWR|unix.O_CREAT|unix.O_APPEND)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Chmod(0600); err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		return err
	}
	size := info.Size()
	var prefix []byte
	if size > 0 {
		var last [1]byte
		if _, err := f.ReadAt(last[:], size-1); err != nil {
			return err
		}
		if last[0] != '\n' {
			prefix = []byte{'\n'}
		}
	}
	var attachments *os.File
	var written []string
	committed := false
	defer func() {
		if attachments == nil {
			return
		}
		defer attachments.Close()
		if !committed {
			for _, name := range written {
				_ = removeAt(attachments, name)
			}
		}
	}()
	if saveImages && len(images) > 0 {
		attachments, err = childDirectory(dir, "attachments", true)
		if err != nil {
			return err
		}
		if err := privateDirectory(attachments); err != nil {
			return err
		}
		for i, image := range images {
			a := &turn.Attachments[i]
			name := fmt.Sprintf("%04d-%02d.%s", index, i+1, strings.ToLower(a.Format))
			if err := atomicWrite(attachments, name, image.Data); err != nil {
				return err
			}
			written = append(written, name)
			a.File = "attachments/" + name
		}
	}
	line, err := encodeTurn(turn)
	if err != nil {
		return err
	}
	if _, err = f.Write(append(append(prefix, line...), '\n')); err == nil {
		err = f.Sync()
	}
	if err == nil {
		err = writeState(dir, state)
	}
	if err != nil {
		if rollbackErr := f.Truncate(size); rollbackErr != nil {
			// A record may still refer to the images, so don't make that worse.
			committed = true
			return errors.Join(err, rollbackErr)
		}
		return errors.Join(err, f.Sync())
	}
	committed = true
	return nil
}
