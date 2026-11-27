package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/iamcheyan/bedrockchat/internal/chat"
)

const legacyID = "abc11100000000000000000000000000"

func TestLegacySessionRoundTrip(t *testing.T) {
	st := Store{Root: filepath.Join(t.TempDir(), "history"), Enabled: true}
	dir := filepath.Join(st.Root, legacyID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(dir, "state.json"), `{"id":"`+legacyID+`","name":"Legacy","model_id":"example-model","context_start":0,"created_at":"2024-01-01T00:00:00.000000+00:00","updated_at":"2024-01-01T00:00:00.000000+00:00"}`)
	writeTestFile(t, filepath.Join(dir, "turns.jsonl"), `{"model_id":"example-model","user":"前 中 後","assistant":"Straße","status":"complete","timestamp":"2024-01-01T00:00:00.000000+00:00","saved_at":"2024-01-01T00:00:00.000000+00:00","attachments":[],"usage":{"input_tokens":7,"output_tokens":8,"total_tokens":15}}`)
	sess, err := st.Load("abc1")
	if err != nil {
		t.Fatal(err)
	}
	if sess.ID != legacyID || sess.Name != "Legacy" || sess.Model != "example-model" || len(sess.Turns) != 1 || sess.Turns[0].User != "前\u2028中\u2029後" || sess.Turns[0].Usage.TotalTokens != 15 {
		t.Fatalf("legacy session: %+v", sess)
	}
	stale := sess
	sess.Name, sess.Model, sess.ContextStart = "改名", "switched-model", 1
	if err := st.State(&sess); err != nil {
		t.Fatal(err)
	}
	next := Turn{Model: sess.Model, User: "new", Assistant: "reply", Timestamp: "2024-01-01T00:00:01.000000+00:00"}
	for i := 0; i < 2; i++ {
		if err := st.SaveTurn(&stale, next, nil); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.Load(legacyID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != sess.Name || got.Model != sess.Model || got.ContextStart != 1 || len(got.Turns) != 2 || got.Turns[1].Status != "complete" || len(stale.Turns) != 2 {
		t.Fatalf("reloaded: %+v", got)
	}
	listed, err := st.List("")
	if err != nil || len(listed) != 1 || len(listed[0].Turns) != 0 {
		t.Fatalf("list: %+v %v", listed, err)
	}
	found, err := st.List("STRASSE")
	if err != nil || !reflect.DeepEqual(found, []Session{got}) {
		t.Fatalf("search: %+v %v", found, err)
	}
	state := readTestFile(t, filepath.Join(dir, "state.json"))
	if !strings.Contains(state, `"created_at":"2024-01-01T00:00:00.000000+00:00"`) {
		t.Fatalf("creation time not preserved: %s", state)
	}
	for _, line := range strings.Split(strings.TrimSpace(readTestFile(t, filepath.Join(dir, "turns.jsonl"))), "\n") {
		if !json.Valid([]byte(line)) {
			t.Fatalf("malformed JSONL line: %q", line)
		}
	}
	if err := st.Delete("abc1"); err != nil {
		t.Fatal(err)
	}
	mustNotExist(t, dir)
}

func TestDisabledStoreKeepsOnlyInMemoryHistory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	st := Store{Root: root, SaveImages: true}
	sess := st.New("", "model")
	if len(sess.ID) != 32 || sess.Name != "未命名" {
		t.Fatalf("new: %+v", sess)
	}
	if err := st.SaveTurn(&sess, Turn{Model: "model", User: "hello"}, []chat.Image{{Data: []byte("payload"), Name: "shot.png", Format: "png", Width: 1, Height: 1}}); err != nil {
		t.Fatal(err)
	}
	if len(sess.Turns) != 1 || len(sess.Turns[0].Attachments) != 1 || sess.Turns[0].Attachments[0].File != "" {
		t.Fatalf("turns: %+v", sess.Turns)
	}
	if err := st.State(&sess); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Load(sess.ID); err == nil {
		t.Fatal("load enabled")
	}
	if _, err := st.List(""); err == nil {
		t.Fatal("list enabled")
	}
	if err := st.Delete(sess.ID); err == nil {
		t.Fatal("delete enabled")
	}
	mustNotExist(t, root)
}

func TestNewSessionPersistsOnlyOnState(t *testing.T) {
	st := Store{Root: filepath.Join(t.TempDir(), "history"), Enabled: true}
	sess := st.New("pending", "")
	mustNotExist(t, st.Root)
	if err := st.State(&sess); err != nil {
		t.Fatal(err)
	}
	got, err := st.Load(sess.ID)
	if err != nil || got.Name != "pending" || got.Model != "" || len(got.Turns) != 0 {
		t.Fatalf("new state: %+v %v", got, err)
	}
	assertMode(t, st.Root, 0700)
	assertMode(t, filepath.Join(st.Root, sess.ID), 0700)
	assertMode(t, filepath.Join(st.Root, sess.ID, "state.json"), 0600)
	assertMode(t, filepath.Join(st.Root, sess.ID, ".lock"), 0600)
}

func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s should not exist: %v", path, err)
	}
}

func assertMode(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != mode {
		t.Fatalf("mode of %s is %o, want %o", path, st.Mode().Perm(), mode)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
