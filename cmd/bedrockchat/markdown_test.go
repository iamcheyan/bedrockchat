package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/glamour"
	"github.com/iamcheyan/bedrockchat/internal/chat"
	"github.com/iamcheyan/bedrockchat/internal/store"
	"github.com/iamcheyan/bedrockchat/internal/terminal"
	"github.com/rivo/uniseg"
)

type replyBackend func(context.Context, chat.Request, func(string)) chat.Result

func (b replyBackend) Send(ctx context.Context, req chat.Request, emit func(string)) chat.Result {
	return b(ctx, req, emit)
}

func TestStreamUsesTransientPreviewAndRendersWholeReply(t *testing.T) {
	renderer, err := glamour.NewTermRenderer(glamour.WithStylePath("dark"))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	h := store.Store{}
	const text = "A short introduction.\n\n| Key | Value |\n| --- | --- |\n| One | `item` |\n\n- *emphasis*\n\n```go\nfmt.Println(42)\n```"
	a := app{cfg: store.Settings{Provider: "openai", MaxInput: 1024, MaxOutput: 64}, history: h,
		session: h.New("fixture", "test.model"), out: &out, lang: "en", color: true, markdown: renderer}
	a.backend = replyBackend(func(_ context.Context, _ chat.Request, emit func(string)) chat.Result {
		emit("A short introduction.")
		if !strings.Contains(out.String(), "\r\x1b[2K") {
			t.Error("stream is not using a transient preview")
		}
		emit(strings.TrimPrefix(text, "A short introduction."))
		return chat.Result{Text: text, StopReason: "end_turn"}
	})
	a.dispatch([]terminal.Part{{Text: "hello"}})
	output := out.String()
	lastClear := strings.LastIndex(output, clearPreview)
	if lastClear == -1 || strings.Contains(output[:lastClear], "\nemphasis") {
		t.Fatal("expected only an erasable preview before the final reply")
	}
	final := output[lastClear+len(clearPreview):]
	if strings.Contains(final, "```go") || strings.Contains(final, "| --- | --- |") || !strings.Contains(final, "│") {
		t.Fatal("expected a rendered table and code block in the final reply")
	}
	if strings.Count(final, "emphasis") != 1 || a.session.Turns[0].Assistant != text {
		t.Fatal("expected one rendered copy and unmodified Markdown in history")
	}
}

func TestPreviewFitsOneRowAndDoesNotSplitGraphemes(t *testing.T) {
	got := previewLine("\x1b[2Jlong line\n中文👩‍💻", 8)
	if got != "…中文👩‍💻" || uniseg.StringWidth(got) != 7 {
		t.Fatalf("preview does not fit: %q", got)
	}
	if got := previewLine("hello", 2); got != "" {
		t.Fatalf("unexpected preview in a two-column terminal: %q", got)
	}
}

func TestMarkdownOutputAllowsOnlyStyleControls(t *testing.T) {
	got := safeMarkdownOutput("\x1b[1mbold\x1b[0m\x1b[2J\a\u009bH")
	if got != "\x1b[1mbold\x1b[0m[2JH" {
		t.Fatalf("unexpected terminal controls: %q", got)
	}
}
