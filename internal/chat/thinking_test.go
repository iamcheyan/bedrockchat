package chat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestBedrockThinkingFields(t *testing.T) {
	for _, tc := range []struct {
		model string
		mode  Thinking
		max   int
		want  string
		fail  bool
	}{
		{"amazon.nova-lite-v1:0", ThinkingAuto, 1024, "", false},
		{"jp.anthropic.claude-haiku-5-5", ThinkingAuto, 1024, "", false},
		{"jp.anthropic.claude-haiku-5-5", ThinkingOn, 1024, `{"thinking":{"type":"adaptive"}}`, false},
		{"arn:aws:bedrock:ap-northeast-1:123456789012:inference-profile/jp.anthropic.claude-haiku-5-5", ThinkingOff, 1024, `{"thinking":{"type":"disabled"}}`, false},
		{"global.anthropic.claude-sonnet-5-5", ThinkingOff, 1024, `{"thinking":{"type":"between_tools"}}`, false},
		{"anthropic.claude-opus-5-5", ThinkingOff, 1024, "", true},
		{"us.anthropic.claude-haiku-4-5-20251001-v1:0", ThinkingOn, 2048, `{"thinking":{"type":"enabled","budget_tokens":1024}}`, false},
		{"us.anthropic.claude-haiku-4-5-20251001-v1:0", ThinkingOff, 1024, "", false},
		{"us.anthropic.claude-haiku-4-5-20251001-v1:0", ThinkingOn, 1024, "", true},
		{"amazon.nova-lite-v1:0", ThinkingOn, 1024, "", true},
	} {
		t.Run(tc.model+"/"+string(tc.mode), func(t *testing.T) {
			req := Request{Model: tc.model, Text: "hi", MaxOutput: tc.max, Thinking: tc.mode}
			input, err := bedrockInput(req)
			if tc.fail {
				if err == nil {
					t.Fatal("unsupported mode or token budget was accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if input.AdditionalModelRequestFields != nil {
					t.Fatal("expected no request fields")
				}
				return
			}
			if input.AdditionalModelRequestFields == nil {
				t.Fatal("missing request fields")
			}
			raw, err := input.AdditionalModelRequestFields.MarshalSmithyDocument()
			var got, want any
			if err != nil || json.Unmarshal(raw, &got) != nil || json.Unmarshal([]byte(tc.want), &want) != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("got %s, want %s (error %v)", raw, tc.want, err)
			}
			if int(*input.InferenceConfig.MaxTokens) != tc.max {
				t.Fatal("output limit changed")
			}
		})
	}
}

func TestBedrockUnsupportedThinkingFailsBeforeCredentialLoading(t *testing.T) {
	t.Setenv("AWS_CONFIG_FILE", t.TempDir()+"/absent")
	b := NewBedrock("", "absent")
	got := b.Send(context.Background(), Request{Model: "amazon.nova-lite-v1:0", Text: "hi", MaxOutput: 1024, Thinking: ThinkingOn}, nil)
	if !errors.Is(got.Err, errThinkingUnsupported) {
		t.Fatalf("expected an unsupported thinking error, got %v", got.Err)
	}
}

func TestOpenAIRejectedThinkingOffersAutoWithoutRetry(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "private service error", http.StatusBadRequest)
	}))
	defer server.Close()
	b := NewOpenAI(server.URL+"/v1/chat/completions", false)
	got := b.Send(context.Background(), Request{Model: "example", Text: "hi", MaxOutput: 64, Thinking: ThinkingOff}, nil)
	if calls != 1 || got.Err == nil || !strings.Contains(got.Err.Error(), "/think auto") || strings.Contains(got.Err.Error(), "private") {
		t.Fatalf("got %d HTTP calls and error %v", calls, got.Err)
	}
}

func TestOpenAIThinkingWireAndNoReasoningText(t *testing.T) {
	for _, tc := range []struct {
		mode Thinking
		want string
	}{{ThinkingAuto, ""}, {ThinkingOn, "low"}, {ThinkingOff, "none"}} {
		t.Run(string(tc.mode), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if got, ok := payload["reasoning_effort"]; tc.want == "" && ok || tc.want != "" && got != tc.want {
					t.Errorf("reasoning_effort = %v, want %q", got, tc.want)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"answer","reasoning_content":"do not display"},"finish_reason":"stop"}]}`))
			}))
			defer server.Close()
			var text strings.Builder
			b := NewOpenAI(server.URL+"/v1/chat/completions", false)
			got := b.Send(context.Background(), Request{Model: "example", Text: "hi", MaxOutput: 1024, Thinking: tc.mode}, func(s string) { text.WriteString(s) })
			if got.Err != nil || got.Text != "answer" || strings.Contains(text.String(), "do not display") {
				t.Fatalf("unexpected response: %+v", got)
			}
		})
	}
}
