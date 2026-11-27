package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/iamcheyan/bedrockchat/internal/chat"
)

func TestDeleteRejectsWorkingDirectoryAsHistoryRoot(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	st := Store{Root: ".", Enabled: true}
	// Even if a matching directory exists, never chmod or remove it through an
	// accidental current-directory root.
	candidate := filepath.Join(dir, legacyID)
	if err := os.Mkdir(candidate, 0700); err != nil {
		t.Fatal(err)
	}
	if err := st.Delete(legacyID); err == nil {
		t.Fatal("deleted outside a dedicated history root")
	}
	assertMode(t, dir, 0755)
	assertMode(t, candidate, 0700)
}

func TestPersistenceRejectsSymlinkedRootsAndRecords(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	if err := os.Mkdir(outside, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{link, filepath.Join(link, "history")} {
		st := Store{Root: root, Enabled: true}
		if err := st.SaveModel("model"); err == nil {
			t.Fatalf("followed symlink %s", root)
		}
	}
	assertMode(t, outside, 0755)
	mustNotExist(t, filepath.Join(outside, "last-model"))
	mustNotExist(t, filepath.Join(outside, "history"))
	st := Store{Root: filepath.Join(base, "history"), Enabled: true}
	sess := st.New("valid", "model")
	if err := st.State(&sess); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(st.Root, sess.ID)
	if err := os.Symlink(outside, filepath.Join(st.Root, legacyID)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Load(legacyID); err == nil {
		t.Fatal("loaded a symlinked session")
	}
	if err := st.Delete(legacyID); err == nil {
		t.Fatal("deleted a symlinked session")
	}
	secret := filepath.Join(outside, "unrelated")
	writeTestFile(t, secret, "outside")
	if err := os.Symlink(secret, filepath.Join(dir, "turns.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveTurn(&sess, Turn{User: "hello"}, nil); err == nil {
		t.Fatal("wrote symlinked record")
	}
	if len(sess.Turns) != 0 {
		t.Fatal("failed write mutated session")
	}
	if got := readTestFile(t, secret); got != "outside" {
		t.Fatal("outside data modified")
	}
	if err := st.Delete(sess.ID); err != nil {
		t.Fatal(err)
	}
	if got := readTestFile(t, secret); got != "outside" {
		t.Fatal("delete followed symlink")
	}
	if err := os.Symlink(secret, filepath.Join(st.Root, "last-model")); err != nil {
		t.Fatal(err)
	}
	if st.LastModel() != "" || st.SaveModel("model") == nil {
		t.Fatal("symlink preference was read or written")
	}
}

func TestFailedImageSaveRollsBackEarlierImages(t *testing.T) {
	st := Store{Root: filepath.Join(t.TempDir(), "history"), Enabled: true, SaveImages: true}
	sess := st.New("images", "model")
	if err := st.State(&sess); err != nil {
		t.Fatal(err)
	}
	ad := filepath.Join(st.Root, sess.ID, "attachments")
	if err := os.Mkdir(ad, 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "not-created")
	if err := os.Symlink(outside, filepath.Join(ad, "0001-02.png")); err != nil {
		t.Fatal(err)
	}
	images := []chat.Image{
		{Name: "one.png", Format: "png", Data: []byte("one")},
		{Name: "two.png", Format: "png", Data: []byte("two")},
	}
	if err := st.SaveTurn(&sess, Turn{User: "question"}, images); err == nil {
		t.Fatal("accepted symlinked image")
	}
	mustNotExist(t, outside)
	mustNotExist(t, filepath.Join(ad, "0001-01.png"))
	reloaded, err := st.Load(sess.ID)
	if err != nil || len(sess.Turns) != 0 || len(reloaded.Turns) != 0 {
		t.Fatalf("failed write added turn: %+v %v", reloaded, err)
	}
}

func TestConcurrentAppendsPreserveEachTurn(t *testing.T) {
	st := Store{Root: filepath.Join(t.TempDir(), "history"), Enabled: true}
	sess := st.New("concurrent", "model")
	if err := st.State(&sess); err != nil {
		t.Fatal(err)
	}
	const writers, turns = 8, 5
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Go(func() {
			local := sess
			for j := 0; j < turns; j++ {
				if err := st.SaveTurn(&local, Turn{User: fmt.Sprintf("worker %d, turn %d", i, j)}, nil); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
	got, err := st.Load(sess.ID)
	if err != nil || len(got.Turns) != writers*turns {
		t.Fatalf("read %d turns, want %d: %v", len(got.Turns), writers*turns, err)
	}
	seen := make(map[string]bool)
	for _, turn := range got.Turns {
		seen[turn.User] = true
	}
	if len(seen) != writers*turns {
		t.Fatalf("read %d unique turns", len(seen))
	}
}

func TestCorruptTurnAndAmbiguousPrefix(t *testing.T) {
	st := Store{Root: filepath.Join(t.TempDir(), "history"), Enabled: true}
	s1, s2 := st.New("one", "model"), st.New("two", "model")
	s1.ID, s2.ID = legacyID, "abc22200000000000000000000000000"
	for _, s := range []*Session{&s1, &s2} {
		if err := st.State(s); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.Load("abc"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("unexpected prefix result: %v", err)
	}
	path := filepath.Join(st.Root, legacyID, "turns.jsonl")
	broken := `{"model_id":"model","user":"hello","assistant":"reply","status":"complete","timestamp":"2024-01-02","saved_at":"2024-01-02","usage":{"input_tokens":1}}`
	writeTestFile(t, path, broken)
	if _, err := st.Load(legacyID); err == nil || !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("unexpected load: %v", err)
	}
	if err := st.SaveTurn(&s1, Turn{User: "new"}, nil); err == nil || readTestFile(t, path) != broken {
		t.Fatalf("appended to corrupt record: %v", err)
	}
	list, err := st.List("")
	if err != nil || len(list) != 1 || list[0].ID != s2.ID {
		t.Fatalf("list: %+v %v", list, err)
	}
}
