package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/iamcheyan/bedrockchat/internal/chat"
	"github.com/iamcheyan/bedrockchat/internal/store"
	"github.com/iamcheyan/bedrockchat/internal/terminal"
	"github.com/rivo/uniseg"
)

type replyBackend func(context.Context, chat.Request, func(string)) chat.Result

func (b replyBackend) Send(ctx context.Context, req chat.Request, emit func(string)) chat.Result {
	return b(ctx, req, emit)
}

func TestStreamRendersMarkdownBeforeReplyFinishes(t *testing.T) {
	renderer, err := newReplyRenderer(80)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	h := store.Store{}
	const text = "## A short introduction.\n\n| Key | Value |\n| --- | --- |\n| One | `item` |\n\n- *emphasis*\n\n```go\nfmt.Println(42)\n```"
	a := app{cfg: store.Settings{Provider: "openai", MaxInput: 1024, MaxOutput: 64}, history: h,
		session: h.New("fixture", "test.model"), out: &out, lang: "en", color: true, markdown: renderer}
	a.backend = replyBackend(func(_ context.Context, _ chat.Request, emit func(string)) chat.Result {
		emit(text)
		live := out.String()
		if !strings.Contains(live, "│") || !strings.Contains(live, "\n") || strings.Contains(live, "| --- | --- |") || strings.Contains(live, "```go") || strings.Contains(live, "## A short introduction.") {
			t.Errorf("reply was not formatted as Markdown while streaming: %q", live)
		}
		return chat.Result{Text: text, StopReason: "end_turn"}
	})
	a.dispatch([]terminal.Part{{Text: "hello"}})
	output := out.String()
	lastClear := strings.LastIndex(output, clearPreview)
	if lastClear == -1 {
		t.Fatal("expected preview to be erased before the final reply")
	}
	final := output[lastClear+len(clearPreview):]
	if strings.Contains(final, "```go") || strings.Contains(final, "| --- | --- |") || !strings.Contains(final, "│") {
		t.Fatal("expected a rendered table and code block in the final reply")
	}
	if strings.Count(final, "emphasis") != 1 || a.session.Turns[0].Assistant != text {
		t.Fatal("expected one rendered copy and unmodified Markdown in history")
	}
}

func TestPreviewRowsWrapAndKeepLatestScreenful(t *testing.T) {
	rows := previewRows("discard this line\n中文👩‍💻\nlast", 8, 3)
	if got := strings.Join(rows, "\n"); got != "中文👩‍💻\nlast" {
		t.Fatalf("wrong visible rows: %q", got)
	}
	rows = previewRows("中文👩‍💻next", 7, 4)
	if len(rows) != 2 || rows[0] != "中文👩‍💻" || rows[1] != "next" || uniseg.StringWidth(rows[0]) != 6 {
		t.Fatalf("did not wrap on a grapheme boundary: %q", rows)
	}
}

func TestMarkdownOutputAllowsOnlyStyleControls(t *testing.T) {
	got := safeMarkdownOutput("\x1b[1mbold\x1b[0m\x1b[2J\a\u009bH")
	if got != "\x1b[1mbold\x1b[0m[2JH" {
		t.Fatalf("unexpected terminal controls: %q", got)
	}
}
