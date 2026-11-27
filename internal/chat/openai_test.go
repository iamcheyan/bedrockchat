package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const completionPath = "/v1/chat/completions"

func TestOpenAISSEVisionAndUsage(t *testing.T) {
	var payload []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != completionPath || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Content-Type") != "application/json" || !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			t.Error("unexpected method, path, or headers")
		}
		var err error
		payload, err = io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		fmt.Fprint(w, "\xef\xbb\xbf: heartbeat\r\n\r\n"+
			"data: {\"choices\":[\r\ndata: {\"delta\":{\"content\":\"hello\",\"reasoning_content\":\"ignored\"}}]}\r\n\r\n"+
			"data: {\"choices\":[{\"delta\":{\"content\":\" world\"},\"finish_reason\":\"stop\"},{\"delta\":{\"content\":\"ignored choice\"}}]}\n\n"+
			"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":0,\"completion_tokens\":3,\"total_tokens\":3}}\n\n"+
			"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":false,\"completion_tokens\":3,\"total_tokens\":3}}\n\n"+
			"data: [DONE]\n\n")
	}))
	defer server.Close()
	req := Request{Model: "vision:latest", Text: "now", System: "system", MaxOutput: 42,
		History: []Pair{{"past", "reply"}}, Images: []Image{{[]byte{1, 2}, "png", 10, 20, "private filename"}}}
	var chunks []string
	got := NewOpenAI(server.URL+completionPath, true).Send(context.Background(), req, func(s string) { chunks = append(chunks, s) })
	if got.Err != nil || got.StopReason != "end_turn" || got.Text != "hello world" || !reflect.DeepEqual(chunks, []string{"hello", " world"}) || got.Usage == nil || *got.Usage != (Usage{0, 3, 3}) {
		t.Fatalf("result=%+v, chunks=%q", got, chunks)
	}
	var actual, want any
	expected := `{"model":"vision:latest","messages":[{"role":"system","content":"system"},{"role":"user","content":"past"},{"role":"assistant","content":"reply"},{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AQI="}},{"type":"text","text":"now"}]}],"max_tokens":42,"stream":true,"stream_options":{"include_usage":true}}`
	if json.Unmarshal(payload, &actual) != nil || json.Unmarshal([]byte(expected), &want) != nil || !reflect.DeepEqual(actual, want) {
		t.Fatalf("unexpected request payload: %s", payload)
	}
}

func TestOpenAIJSON(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request["stream"] != stream || (request["stream_options"] != nil) != stream {
					t.Error("unexpected stream options")
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"choices":[{"message":{"content":"text","reasoning":"private"},"finish_reason":"length"}],"usage":{"prompt_tokens":2,"completion_tokens":4,"total_tokens":6}}`)
			}))
			defer server.Close()
			got := NewOpenAI(server.URL+completionPath, stream).Send(context.Background(), Request{Model: "m", Text: "x", MaxOutput: 1}, nil)
			if got.Err != nil || got.StopReason != "max_tokens" || got.Text != "text" || got.Usage == nil || *got.Usage != (Usage{2, 4, 6}) {
				t.Fatalf("result=%+v", got)
			}
		})
	}
}

func TestOpenAIRejectsUnsafeEndpointsAndRedirects(t *testing.T) {
	req := Request{Model: "m", Text: "private prompt", MaxOutput: 1}
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+completionPath, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	for _, endpoint := range []string{target.URL, target.URL + completionPath + "?private", target.URL + completionPath + "#private", strings.Replace(target.URL, "://", "://user:private@", 1) + completionPath, "http://example.invalid" + completionPath, redirect.URL + completionPath} {
		got := NewOpenAI(endpoint, false).Send(context.Background(), req, nil)
		if got.Err == nil || got.StopReason != "error" || strings.Contains(got.Err.Error(), "private") {
			t.Fatalf("unexpected result: %+v", got)
		}
	}
	if hits.Load() != 0 {
		t.Fatal("disallowed request reached target")
	}
}

func TestOpenAISSEFailurePreservesPartialReply(t *testing.T) {
	for _, tc := range []struct{ name, suffix, stop string }{
		{"missing done", "", "error"},
		{"service error", "data: {\"error\":{\"message\":\"private\"}}\n\n", "error"},
		{"unknown stop", "data: {\"choices\":[{\"finish_reason\":\"private\"}]}\n\ndata: [DONE]\n\n", "error"},
		{"tool stop", "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"function\":{\"arguments\":\"private\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", "tool_use"},
		{"limit", ":" + strings.Repeat("x", 8<<20), "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"part\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\n")
				w.(http.Flusher).Flush()
				fmt.Fprint(w, tc.suffix)
			}))
			defer server.Close()
			got := NewOpenAI(server.URL+completionPath, true).Send(context.Background(), Request{Model: "m", Text: "x", MaxOutput: 1}, nil)
			if got.Err == nil || strings.Contains(got.Err.Error(), "private") || got.StopReason != tc.stop || got.Text != "part" || got.Usage == nil || *got.Usage != (Usage{2, 1, 3}) {
				t.Fatalf("result=%+v", got)
			}
		})
	}
}

func TestOpenAISSECancelClosesResponse(t *testing.T) {
	closed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"part\"}}]}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
			close(closed)
		case <-time.After(3 * time.Second):
			t.Error("handler did not observe disconnect")
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got := NewOpenAI(server.URL+completionPath, true).Send(ctx, Request{Model: "m", Text: "x", MaxOutput: 1}, func(string) { cancel() })
	if got.StopReason != "interrupted" || !errors.Is(got.Err, context.Canceled) || got.Text != "part" {
		t.Fatalf("result=%+v", got)
	}
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not observe disconnect")
	}
}
