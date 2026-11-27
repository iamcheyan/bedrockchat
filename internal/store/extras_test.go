package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iamcheyan/bedrockchat/internal/chat"
)

func TestLegacyPreferences(t *testing.T) {
	root := t.TempDir()
	st := Store{Root: root, Enabled: true}
	writeTestFile(t, filepath.Join(root, "last-model"), "example-model\n")
	writeTestFile(t, filepath.Join(root, "ui-language"), "ja")
	if st.LastModel() != "example-model" || st.Language() != "ja" {
		t.Fatal("legacy preferences did not load")
	}
	if err := st.SaveModel("example/v2:latest"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveLanguage("en"); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"last-model": "example/v2:latest\n", "ui-language": "en\n"} {
		path := filepath.Join(root, name)
		if got := readTestFile(t, path); got != want {
			t.Fatalf("%s: %q, want %q", name, got, want)
		}
		assertMode(t, path, 0600)
	}
	if err := st.SaveLanguage("es"); err == nil {
		t.Fatal("invalid language accepted")
	}
	if err := st.SaveModel("model\nmodel"); err == nil {
		t.Fatal("invalid model accepted")
	}
	st.Enabled = false
	if st.LastModel() != "" || st.Language() != "" {
		t.Fatal("disabled preferences read from disk")
	}
	if err := st.SaveModel("other"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveLanguage("zh"); err != nil {
		t.Fatal(err)
	}
	st.Enabled = true
	if st.LastModel() != "example/v2:latest" || st.Language() != "en" {
		t.Fatal("disabled preferences written to disk")
	}
}

func TestSavedImagePrivateAndReusable(t *testing.T) {
	st := Store{Root: filepath.Join(t.TempDir(), "history"), Enabled: true, SaveImages: true}
	sess := st.New("image", "model")
	img := chat.Image{Data: []byte("payload"), Format: "PNG", Width: 8, Height: 6, Name: "画像.png"}
	if err := st.SaveTurn(&sess, Turn{User: "image", Assistant: "reply"}, []chat.Image{img}); err != nil {
		t.Fatal(err)
	}
	if len(sess.Turns) != 1 || len(sess.Turns[0].Attachments) != 1 || sess.Turns[0].Attachments[0].File != "attachments/0001-01.png" {
		t.Fatalf("session attachment not populated: %+v", sess)
	}
	loaded, err := st.Load(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	a := loaded.Turns[0].Attachments[0]
	if a.Size != len(img.Data) || a.Width != img.Width || a.Height != img.Height || a.Name != img.Name {
		t.Fatalf("attachment metadata: %+v", a)
	}
	st.SaveImages = false // reuse still works when writing new files is disabled
	path, err := st.Attachment(loaded, 1, 1)
	if err != nil || path != filepath.Join(st.Root, sess.ID, "attachments", "0001-01.png") || readTestFile(t, path) != "payload" {
		t.Fatalf("attachment: %s,%v", path, err)
	}
	assertMode(t, filepath.Dir(path), 0700)
	assertMode(t, path, 0600)
	assertMode(t, filepath.Join(st.Root, sess.ID, "turns.jsonl"), 0600)
	if err := st.SaveTurn(&sess, Turn{User: "no bytes"}, []chat.Image{img}); err != nil {
		t.Fatal(err)
	}
	mustNotExist(t, filepath.Join(filepath.Dir(path), "0002-01.png"))
	if _, err := st.Attachment(sess, 2, 1); err == nil {
		t.Fatal("unsaved attachment accepted")
	}
	if _, err := st.Attachment(sess, 0, 1); err == nil {
		t.Fatal("zero-based turn accepted")
	}
}

func TestExportWithHistoryDisabledNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	st := Store{Root: filepath.Join(dir, "history")}
	sess := st.New("書き出し", "model")
	if err := st.SaveTurn(&sess, Turn{User: "question", Assistant: "reply", Status: "incomplete", Usage: &chat.Usage{InputTokens: 7, OutputTokens: 8, TotalTokens: 15}}, nil); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "chat.md")
	if err := st.Export(sess, path); err != nil {
		t.Fatal(err)
	}
	contents := readTestFile(t, path)
	for _, want := range []string{"# 書き出し", "## Turn 1", "question", "reply", "incomplete", "input_tokens=7", "total_tokens=15"} {
		if !strings.Contains(contents, want) {
			t.Errorf("export missing %q", want)
		}
	}
	assertMode(t, path, 0600)
	if err := st.Export(sess, path); err == nil {
		t.Fatal("export overwrote existing file")
	}
	if got := readTestFile(t, path); got != contents {
		t.Fatal("export changed")
	}
	busted := filepath.Join(dir, "absent.md")
	if err := os.Symlink(busted, filepath.Join(dir, "link.md")); err != nil {
		t.Fatal(err)
	}
	if err := st.Export(sess, filepath.Join(dir, "link.md")); err == nil {
		t.Fatal("export followed dangling symlink")
	}
	mustNotExist(t, busted)
	mustNotExist(t, st.Root)
}
