package terminal

import (
	"bytes"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/iamcheyan/bedrockchat/internal/chat"
)

func withKeys(t *testing.T, keys string) (*Editor, *bytes.Buffer) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	if _, err := io.WriteString(w, keys); err != nil {
		t.Fatal(err)
	}
	w.Close()
	var out bytes.Buffer
	return New(r, &out), &out
}

func requireParts(t *testing.T, got []Part, err error, want ...Part) {
	t.Helper()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, %v; want %#v", got, err, want)
	}
}

func TestMultilineEditAndBracketedPaste(t *testing.T) {
	ed, out := withKeys(t, "日本\x1b[D語\x1b[F\x1b\r二\x1b[200~\r\n三\r四\x07\x03\x1b[201~\r")
	parts, err := ed.Read(nil, func() (chat.Image, error) {
		t.Fatal("bracketed text paste must not invoke clipboard callback")
		return chat.Image{}, nil
	}, nil)
	requireParts(t, parts, err, Part{Text: "日語本\n二\n三\n四"})
	if !strings.Contains(out.String(), "› ") || strings.Contains(out.String(), "You") {
		t.Fatalf("unexpected prompt: %q", out.String())
	}
}

func TestUnicodeBackspaceRemovesGrapheme(t *testing.T) {
	ed, _ := withKeys(t, "a👩‍💻\x7fe\u0301\x7f\r")
	parts, err := ed.Read(nil, nil, nil)
	requireParts(t, parts, err, Part{Text: "a"})
}

func TestAtomicImagesAndMiddleInsertion(t *testing.T) {
	old := chat.Image{Name: "old.png"}
	fresh := chat.Image{Name: "fresh.png"}
	for _, tt := range []struct {
		name, keys string
		want       []Part
	}{
		{"kept in order", "\x1b[D\x07\r", []Part{{Text: "a"}, {Image: &old}, {Image: &fresh}, {Text: "b"}}},
		{"backspace and Delete", "\x1b[D\x7f\x07\x1b[D\x1b[3~\r", []Part{{Text: "ab"}}},
		{"chord drops newest even before old", "\x1b[D\x1b[D\x07\x18\x04\r", []Part{{Text: "a"}, {Image: &old}, {Text: "b"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ed, out := withKeys(t, tt.keys)
			ed.ImageLabel = func(img chat.Image) string { return "【画像 " + img.Name + "】" }
			calls := 0
			parts, err := ed.Read([]Part{{Text: "a"}, {Image: &old}, {Text: "b"}}, func() (chat.Image, error) {
				calls++
				return fresh, nil
			}, nil)
			requireParts(t, parts, err, tt.want...)
			if calls != 1 || strings.Contains(out.String(), "【画像 fresh.png】") != (tt.name == "kept in order") {
				t.Fatalf("callback count %d; output %q", calls, out.String())
			}
		})
	}
}

func TestHistorySearchAndReuse(t *testing.T) {
	ed, _ := withKeys(t, "\x1b[A\x1b[A\x1b[B\x1b[B\r\x12猫\x12\r!\r\x03\x04")
	img := chat.Image{Name: "keep.png"}
	ed.Remember("older 猫")
	ed.Remember("newer 猫")
	parts, err := ed.Read([]Part{{Text: "draft"}, {Image: &img}}, nil, nil)
	requireParts(t, parts, err, Part{Text: "draft"}, Part{Image: &img})
	parts, err = ed.Read([]Part{{Image: &img}}, nil, nil)
	requireParts(t, parts, err, Part{Text: "older 猫!"}, Part{Image: &img})
	if parts, err = ed.Read(nil, nil, nil); len(parts) != 0 || !errors.Is(err, ErrInterrupted) {
		t.Fatalf("expected interruption, got %v, %v", parts, err)
	}
	if _, err := ed.Read(nil, nil, nil); !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestPasteErrorPreservesDraft(t *testing.T) {
	ed, _ := withKeys(t, "a\x07b\r")
	want := errors.New("clipboard error")
	var seen error
	parts, err := ed.Read(nil, func() (chat.Image, error) { return chat.Image{}, want }, func(err error) { seen = err })
	requireParts(t, parts, err, Part{Text: "ab"})
	if seen != want {
		t.Fatalf("callback error = %v, want %v", seen, want)
	}
}
