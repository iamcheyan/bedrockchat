package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/iamcheyan/bedrockchat/internal/chat"
	"github.com/iamcheyan/bedrockchat/internal/store"
	"github.com/iamcheyan/bedrockchat/internal/terminal"
)

type testBackend struct{ requests []chat.Request }

func TestResponseSeparatesMutedStatusFromBody(t *testing.T) {
	for _, color := range []bool{true, false} {
		var out bytes.Buffer
		h := store.Store{}
		a := app{cfg: store.Settings{Provider: "openai", MaxInput: 1024, MaxOutput: 64}, history: h, session: h.New("fixture", "test.model"), backend: &testBackend{}, out: &out, lang: "en", color: color}
		a.showModel()
		a.dispatch([]terminal.Part{{Text: "hello"}})
		a.dispatch([]terminal.Part{{Text: "/show"}})
		str := out.String()
		for _, status := range []string{"Current model: test.model", "Estimated input:", "Request tokens:", "Session tokens:", "#1 ·"} {
			prefix := ""
			if color {
				prefix = "\x1b[90m"
			}
			if !strings.Contains(str, prefix+status) {
				t.Errorf("color=%t: missing status %q in %q", color, status, str)
			}
		}
		body := "\noffline reply\n"
		if strings.Count(str, body) != 2 || !strings.Contains(str, "\nhello\n") {
			t.Errorf("color=%t: model or user body was styled: %q", color, str)
		}
		if color && !strings.Contains(str, "\x1b[39m\noffline reply\n\x1b[90mRequest tokens:") {
			t.Errorf("terminal foreground not reset before reply: %q", str)
		}
		if !color && strings.ContainsRune(str, '\x1b') {
			t.Errorf("unexpected escapes in plain output: %q", str)
		}
	}
}

func (b *testBackend) Send(_ context.Context, req chat.Request, delta func(string)) chat.Result {
	b.requests = append(b.requests, req)
	delta("offline reply")
	return chat.Result{Text: "offline reply", StopReason: "end_turn", Usage: &chat.Usage{InputTokens: 5, OutputTokens: 3, TotalTokens: 8}}
}

func TestLocalModelCommandsAndMultimodalContext(t *testing.T) {
	var out bytes.Buffer
	backend := &testBackend{}
	h := store.Store{Root: t.TempDir(), Enabled: true}
	a := app{cfg: store.Settings{Provider: "openai", MaxInput: 1024, MaxOutput: 64}, history: h, prefs: h, session: h.New("fixture", "test.first"), backend: backend, out: &out, lang: "en"}
	a.dispatch([]terminal.Part{{Text: "/model"}})
	a.dispatch([]terminal.Part{{Text: "/model test.next"}})
	if len(backend.requests) != 0 || h.LastModel() != "test.next" || !strings.Contains(out.String(), "Current model: test.next") {
		t.Fatalf("model state not updated: %s", out.String())
	}
	image := chat.Image{Data: []byte("test"), Format: "png", Name: "sample.png", Width: 10, Height: 10}
	a.dispatch([]terminal.Part{{Text: "left "}, {Image: &image}, {Text: "right"}})
	a.dispatch([]terminal.Part{{Text: "follow-up"}})
	if len(backend.requests) != 2 {
		t.Fatalf("got %d requests", len(backend.requests))
	}
	first, second := backend.requests[0], backend.requests[1]
	if first.Text != "left right" || len(first.Images) != 1 || second.Model != "test.next" || len(second.Images) != 0 || len(second.History) != 1 || second.History[0].User != "left right" {
		t.Fatalf("unexpected payloads: %+v", backend.requests)
	}
	if !strings.Contains(out.String(), "total 16") {
		t.Fatalf("missing session usage: %s", out.String())
	}
}

func TestVersionAndCurrentModelDoNotNeedTerminal(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var out, err bytes.Buffer
	if code := run([]string{"--version"}, nil, &out, &err); code != 0 || out.String() != "bedrockchat "+version+"\n" {
		t.Fatalf("version: %d, %s, %s", code, &out, &err)
	}
	out.Reset()
	if code := run([]string{"--current-model", "--provider", "openai", "--model", "test.model"}, nil, &out, &err); code != 0 || out.String() != "test.model\n" {
		t.Fatalf("model: %d, %s, %s", code, &out, &err)
	}
}
