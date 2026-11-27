package chat

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/logging"
	"github.com/aws/smithy-go/metrics"
	"github.com/aws/smithy-go/tracing"
)

type sdkReader struct {
	events chan types.ConverseStreamOutput
	err    error
	closed bool
}

func (r *sdkReader) Events() <-chan types.ConverseStreamOutput { return r.events }
func (r *sdkReader) Close() error                              { r.closed = true; return nil }
func (r *sdkReader) Err() error                                { return r.err }

func sdkText(text string) types.ConverseStreamOutput {
	return &types.ConverseStreamOutputMemberContentBlockDelta{Value: types.ContentBlockDeltaEvent{Delta: &types.ContentBlockDeltaMemberText{Value: text}}}
}

func sdkEvents(err error, events ...types.ConverseStreamOutput) *sdkReader {
	r := &sdkReader{events: make(chan types.ConverseStreamOutput, len(events)), err: err}
	for _, event := range events {
		r.events <- event
	}
	close(r.events)
	return r
}

func sdkStream(r *sdkReader) *bedrockruntime.ConverseStreamEventStream {
	return bedrockruntime.NewConverseStreamEventStream(func(es *bedrockruntime.ConverseStreamEventStream) { es.Reader = r })
}

func TestBedrockVisionRequestAndTextEvents(t *testing.T) {
	req := Request{Model: "us.amazon.nova-lite-v1:0", System: "system", Text: "current", MaxOutput: 42,
		History: []Pair{{"past", "reply"}}, Images: []Image{{[]byte{1, 2}, "png", 15, 20, "private filename"}}}
	r := sdkEvents(nil, sdkText("one"), &types.ConverseStreamOutputMemberContentBlockDelta{Value: types.ContentBlockDeltaEvent{Delta: &types.ContentBlockDeltaMemberToolUse{Value: types.ToolUseBlockDelta{Input: aws.String("discard me")}}}},
		&types.ConverseStreamOutputMemberContentBlockDelta{Value: types.ContentBlockDeltaEvent{Delta: &types.ContentBlockDeltaMemberReasoningContent{}}},
		sdkText(" two"), &types.ConverseStreamOutputMemberMessageStop{Value: types.MessageStopEvent{StopReason: types.StopReasonEndTurn}},
		&types.ConverseStreamOutputMemberMetadata{Value: types.ConverseStreamMetadataEvent{Usage: &types.TokenUsage{InputTokens: aws.Int32(12), OutputTokens: aws.Int32(2), TotalTokens: aws.Int32(14)}}},
		&types.ConverseStreamOutputMemberMetadata{Value: types.ConverseStreamMetadataEvent{Usage: &types.TokenUsage{InputTokens: aws.Int32(-1)}}})
	backend := NewBedrock("", "")
	backend.client = func(_ context.Context, input *bedrockruntime.ConverseStreamInput) (*bedrockruntime.ConverseStreamEventStream, error) {
		want := &bedrockruntime.ConverseStreamInput{ModelId: aws.String(req.Model), InferenceConfig: &types.InferenceConfiguration{MaxTokens: aws.Int32(42)},
			System: []types.SystemContentBlock{&types.SystemContentBlockMemberText{Value: "system"}},
			Messages: []types.Message{
				{Role: types.ConversationRoleUser, Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: "past"}}},
				{Role: types.ConversationRoleAssistant, Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: "reply"}}},
				{Role: types.ConversationRoleUser, Content: []types.ContentBlock{&types.ContentBlockMemberImage{Value: types.ImageBlock{Format: types.ImageFormatPng, Source: &types.ImageSourceMemberBytes{Value: []byte{1, 2}}}}, &types.ContentBlockMemberText{Value: "current"}}},
			}}
		if !reflect.DeepEqual(input, want) {
			t.Fatalf("wrong SDK input: %+v", input)
		}
		return sdkStream(r), nil
	}
	var chunks []string
	got := backend.Send(context.Background(), req, func(chunk string) { chunks = append(chunks, chunk) })
	if got.Err != nil || got.StopReason != "end_turn" || got.Text != "one two" || !reflect.DeepEqual(chunks, []string{"one", " two"}) || got.Usage == nil || *got.Usage != (Usage{12, 2, 14}) || !r.closed {
		t.Fatalf("result=%+v, chunks=%q, closed=%t", got, chunks, r.closed)
	}
}

func TestBedrockIncompleteAndSafeStreamErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		stop types.StopReason
		err  error
		want string
	}{
		{"no stop", "", nil, "error"},
		{"unknown stop", "private", nil, "error"},
		{"tool", types.StopReasonToolUse, nil, "tool_use"},
		{"filter", types.StopReasonContentFiltered, nil, "content_filtered"},
		{"service error", types.StopReasonEndTurn, &smithy.GenericAPIError{Code: "modelStreamErrorException", Message: "private"}, "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := sdkEvents(tc.err, sdkText("part"), &types.ConverseStreamOutputMemberMessageStop{Value: types.MessageStopEvent{StopReason: tc.stop}})
			b := NewBedrock("", "")
			b.client = func(context.Context, *bedrockruntime.ConverseStreamInput) (*bedrockruntime.ConverseStreamEventStream, error) {
				return sdkStream(r), nil
			}
			got := b.Send(context.Background(), Request{Model: "m", Text: "x", MaxOutput: 10}, nil)
			if got.StopReason != tc.want || got.Err == nil || strings.Contains(got.Err.Error(), "private") || got.Text != "part" || !r.closed {
				t.Fatalf("result=%+v", got)
			}
			if tc.err != nil && !strings.Contains(got.Err.Error(), "stream interrupted") {
				t.Fatalf("error not mapped: %v", got.Err)
			}
		})
	}
}

func TestBedrockCancellationAndValidation(t *testing.T) {
	b := NewBedrock("", "")
	r := &sdkReader{events: make(chan types.ConverseStreamOutput, 1)}
	r.events <- sdkText("part")
	calls := 0
	b.client = func(context.Context, *bedrockruntime.ConverseStreamInput) (*bedrockruntime.ConverseStreamEventStream, error) {
		calls++
		return sdkStream(r), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if got := b.Send(ctx, Request{Model: "arn:aws:bedrock:us-west-2:123:prompt/name", Text: "x", MaxOutput: 1}, nil); got.Err == nil || calls != 0 {
		t.Fatalf("invalid model sent: %+v", got)
	}
	if math.MaxInt > math.MaxInt32 {
		max := int64(math.MaxInt32) + 1
		if got := b.Send(ctx, Request{Model: "m", Text: "x", MaxOutput: int(max)}, nil); got.Err == nil || calls != 0 {
			t.Fatalf("overflow sent: %+v", got)
		}
	}
	got := b.Send(ctx, Request{Model: "m", Text: "x", MaxOutput: 10}, func(string) { cancel() })
	if got.StopReason != "interrupted" || !errors.Is(got.Err, context.Canceled) || got.Text != "part" || !r.closed || calls != 1 {
		t.Fatalf("result=%+v", got)
	}
}

func TestBedrockMissingSSOSessionUsesSafeLoginHint(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "config")
	fixture := "[profile fixture]\nregion = us-east-1\nsso_session = fixture\nsso_account_id = 123456789012\nsso_role_name = SampleRole\n[sso-session fixture]\nsso_region = us-east-1\nsso_start_url = https://example.invalid/start\n"
	if err := os.WriteFile(configFile, []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("AWS_CONFIG_FILE", configFile)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "")
	b := NewBedrock("us-east-1", "fixture")
	got := b.Send(context.Background(), Request{Model: "model", Text: "test", MaxOutput: 1}, nil)
	if got.StopReason != "error" || got.Err == nil || !strings.Contains(got.Err.Error(), "aws sso login") || strings.Contains(got.Err.Error(), dir) {
		t.Fatalf("unexpected SSO result: %+v", got)
	}
}

func TestBedrockProfileSelectionMatchesPython(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config")
	credsPath := filepath.Join(dir, "credentials")
	if err := os.WriteFile(configPath, []byte("[profile legacy]\nregion = ap-northeast-1\n[profile chosen]\nregion = us-west-2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credsPath, []byte("[legacy]\naws_access_key_id = fixture-legacy\naws_secret_access_key = fixture\n[chosen]\naws_access_key_id = fixture-chosen\naws_secret_access_key = fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_CONFIG_FILE", configPath)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credsPath)
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	for _, key := range []string{"AWS_REGION", "AWS_DEFAULT_REGION", "AWS_ACCESS_KEY_ID", "AWS_ACCESS_KEY", "AWS_SECRET_ACCESS_KEY", "AWS_SECRET_KEY", "AWS_SESSION_TOKEN", "AWS_BEARER_TOKEN_BEDROCK", "AWS_WEB_IDENTITY_TOKEN_FILE"} {
		t.Setenv(key, "")
	}
	for _, tc := range []struct {
		name, profile, defaultProfile, explicit, envKey, wantRegion, wantKey string
	}{
		{"old shell name missing", "missing", "legacy", "", "", "ap-northeast-1", "fixture-legacy"},
		{"both exist", "chosen", "legacy", "", "", "ap-northeast-1", "fixture-legacy"},
		{"AWS_PROFILE alone", "chosen", "", "", "", "us-west-2", "fixture-chosen"},
		{"environment keys still win", "missing", "legacy", "", "fixture-env", "ap-northeast-1", "fixture-env"},
		{"explicit profile wins over environment", "missing", "legacy", "chosen", "fixture-env", "us-west-2", "fixture-chosen"},
		{"missing explicit profile never falls back", "chosen", "legacy", "missing", "fixture-env", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AWS_PROFILE", tc.profile)
			t.Setenv("AWS_DEFAULT_PROFILE", tc.defaultProfile)
			if tc.envKey != "" {
				t.Setenv("AWS_ACCESS_KEY_ID", tc.envKey)
				t.Setenv("AWS_SECRET_ACCESS_KEY", "fixture")
			}
			client, err := makeBedrockClient(context.Background(), "", tc.explicit)
			if tc.wantKey == "" {
				if err == nil || !strings.Contains(bedrockError(err, false).Error(), "AWS profile not found") {
					t.Fatal("a missing explicit profile must fail, not silently change accounts")
				}
				return
			}
			if err != nil {
				t.Fatal(bedrockError(err, false))
			}
			opts := client.Options()
			creds, err := opts.Credentials.Retrieve(context.Background())
			if err != nil || creds.AccessKeyID != tc.wantKey || opts.Region != tc.wantRegion {
				t.Fatalf("wrong region or credential source (region=%s, source=%s, error=%v)", opts.Region, creds.Source, err)
			}
			if os.Getenv("AWS_PROFILE") != tc.profile || os.Getenv("AWS_DEFAULT_PROFILE") != tc.defaultProfile {
				t.Fatal("profile resolution modified the process environment")
			}
		})
	}
	t.Run("environment web identity is not replaced by profile credentials", func(t *testing.T) {
		t.Setenv("AWS_PROFILE", "chosen")
		t.Setenv("AWS_DEFAULT_PROFILE", "legacy")
		t.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", filepath.Join(dir, "nonexistent-token"))
		t.Setenv("AWS_ROLE_ARN", "arn:aws:iam::123456789012:role/fixture")
		client, err := makeBedrockClient(context.Background(), "", "")
		if err != nil {
			t.Fatal(bedrockError(err, false))
		}
		if _, err := client.Options().Credentials.Retrieve(context.Background()); !errors.Is(err, errAWSNoCredentials) {
			t.Fatal("expected the missing web identity token to fail without falling back to static credentials")
		}
	})
}

func TestBedrockSDKConfiguration(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "config")
	if err := os.WriteFile(configFile, []byte("[profile fixture]\nregion = eu-west-1\nendpoint_url = https://example.invalid\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_CONFIG_FILE", configFile)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_REGION", "us-west-1")
	t.Setenv("AWS_DEFAULT_REGION", "us-east-1")
	t.Setenv("AWS_ENDPOINT_URL", "https://example.invalid")
	t.Setenv("AWS_ENDPOINT_URL_BEDROCK_RUNTIME", "https://example.invalid")
	t.Setenv("AWS_APP_ID", "private")
	for _, tc := range []struct{ region, want string }{{"", "us-west-1"}, {"ap-northeast-1", "ap-northeast-1"}} {
		client, err := makeBedrockClient(context.Background(), tc.region, "fixture")
		if err != nil {
			t.Fatal(err)
		}
		opts := client.Options()
		endpoint, endpointErr := opts.EndpointResolverV2.ResolveEndpoint(context.Background(), bedrockruntime.EndpointParameters{Region: aws.String(tc.want)})
		_, loggerOK := opts.Logger.(logging.Nop)
		_, tracerOK := opts.TracerProvider.(tracing.NopTracerProvider)
		_, metricsOK := opts.MeterProvider.(metrics.NopMeterProvider)
		if endpointErr != nil || strings.Contains(endpoint.URI.String(), "example.invalid") || opts.Region != tc.want || opts.BaseEndpoint != nil || opts.Retryer.MaxAttempts() != 1 || opts.AppID != "" || !loggerOK || !tracerOK || !metricsOK {
			t.Fatal("SDK did not honor region, endpoint, retry, or privacy settings")
		}
	}
}
