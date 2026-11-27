package chat

import (
	"context"
	"errors"
	"math"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/logging"
	"github.com/aws/smithy-go/metrics"
	"github.com/aws/smithy-go/tracing"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

type converseStream func(context.Context, *bedrockruntime.ConverseStreamInput) (*bedrockruntime.ConverseStreamEventStream, error)

type Bedrock struct {
	region, profile string
	mu              sync.Mutex
	client          converseStream
}

// NewBedrock loads AWS configuration on the first valid send. An empty region
// or profile uses the AWS SDK's usual region and credential resolution.
func NewBedrock(region, profile string) *Bedrock {
	return &Bedrock{region: region, profile: profile}
}

func makeBedrockClient(ctx context.Context, region, profile string) (*bedrockruntime.Client, error) {
	dialer := &net.Dialer{Timeout: connectTimeout}
	transport := awshttp.NewBuildableClient().WithTransportOptions(func(tr *http.Transport) {
		tr.DialContext = timedDial(dialer)
		tr.TLSHandshakeTimeout = connectTimeout
		tr.ResponseHeaderTimeout = ioTimeout
	})
	options := []func(*config.LoadOptions) error{
		config.WithRegion(region), config.WithSharedConfigProfile(profile),
		config.WithDefaultsMode(aws.DefaultsModeStandard),
		config.WithRetryer(func() aws.Retryer { return aws.NopRetryer{} }),
		config.WithLogger(logging.Nop{}), config.WithClientLogMode(0),
		config.WithLogConfigurationWarnings(false), config.WithHTTPClient(transport),
	}
	// Botocore prefers AWS_DEFAULT_PROFILE when both environment variables
	// are set; the Go SDK prefers AWS_PROFILE. Align the implicit selection
	// without changing the process environment or a caller's explicit profile.
	if legacy, selected := os.Getenv("AWS_DEFAULT_PROFILE"), os.Getenv("AWS_PROFILE"); profile == "" && legacy != "" && selected != "" && selected != legacy {
		env, err := config.NewEnvConfig()
		if err != nil {
			return nil, err
		}
		// The SDK treats the override as an explicit profile. Preserve static
		// environment credentials, and leave the SDK's environment-driven web
		// identity chain alone rather than replacing it with profile credentials.
		if env.Credentials.HasKeys() || env.WebIdentityTokenFilePath == "" {
			options = append(options, config.WithSharedConfigProfile(legacy))
			if env.Credentials.HasKeys() {
				creds := env.Credentials
				options = append(options, config.WithCredentialsProvider(aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
					return creds, nil
				})))
			}
		}
	}
	cfg, err := config.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, err
	}
	if cfg.Region == "" {
		return nil, errAWSRegion
	}
	// The generated endpoint resolver reads endpoint variables from config
	// sources at request time. Ignore those sources while keeping standard
	// region-based AWS endpoint resolution.
	ignoreEndpoints := true
	for i, source := range cfg.ConfigSources {
		switch value := source.(type) {
		case config.EnvConfig:
			value.BaseEndpoint = ""
			value.IgnoreConfiguredEndpoints = &ignoreEndpoints
			cfg.ConfigSources[i] = value
		case config.SharedConfig:
			value.BaseEndpoint = ""
			value.IgnoreConfiguredEndpoints = &ignoreEndpoints
			cfg.ConfigSources[i] = value
		}
	}
	if provider := cfg.Credentials; provider != nil {
		// Keep provider failures (which can include private command output and
		// token cache paths) out of displayed errors.
		credentialErr := errAWSNoCredentials
		if source, ok := provider.(aws.CredentialProviderSource); ok && slices.ContainsFunc(source.ProviderSources(), isSSOSource) {
			credentialErr = errAWSReauth
		}
		cfg.Credentials = aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
			creds, err := provider.Retrieve(ctx)
			if err != nil || !creds.HasKeys() {
				return aws.Credentials{}, credentialErr
			}
			return creds, nil
		})
	}
	return bedrockruntime.NewFromConfig(cfg, func(o *bedrockruntime.Options) {
		// Override both environment and profile endpoints, retaining normal SDK
		// endpoint resolution. Do not redirect prompts or signed requests.
		o.BaseEndpoint = nil
		o.AppID = ""
		o.TracerProvider = tracing.NopTracerProvider{}
		o.MeterProvider = metrics.NopMeterProvider{}
		o.HTTPClient = &http.Client{Transport: cfg.HTTPClient.(*awshttp.BuildableClient).GetTransport(), CheckRedirect: noRedirect}
	}), nil
}

func isSSOSource(source aws.CredentialSource) bool {
	switch source {
	case aws.CredentialSourceSSO, aws.CredentialSourceProfileSSO, aws.CredentialSourceSSOLegacy, aws.CredentialSourceProfileSSOLegacy:
		return true
	default:
		return false
	}
}

func (b *Bedrock) getClient(ctx context.Context) (converseStream, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.client == nil {
		c, err := makeBedrockClient(ctx, b.region, b.profile)
		if err != nil {
			return nil, err
		}
		b.client = func(ctx context.Context, input *bedrockruntime.ConverseStreamInput) (*bedrockruntime.ConverseStreamEventStream, error) {
			out, err := c.ConverseStream(ctx, input)
			if err != nil || out == nil {
				return nil, err
			}
			return out.GetStream(), nil
		}
	}
	return b.client, nil
}

func bedrockInput(req Request) *bedrockruntime.ConverseStreamInput {
	input := &bedrockruntime.ConverseStreamInput{
		ModelId:         aws.String(req.Model),
		InferenceConfig: &types.InferenceConfiguration{MaxTokens: aws.Int32(int32(req.MaxOutput))},
	}
	if req.System != "" {
		input.System = []types.SystemContentBlock{&types.SystemContentBlockMemberText{Value: req.System}}
	}
	textMessage := func(role types.ConversationRole, text string) types.Message {
		return types.Message{Role: role, Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: text}}}
	}
	for _, turn := range req.History {
		input.Messages = append(input.Messages, textMessage(types.ConversationRoleUser, turn.User), textMessage(types.ConversationRoleAssistant, turn.Assistant))
	}
	current := types.Message{Role: types.ConversationRoleUser}
	for _, img := range req.Images {
		current.Content = append(current.Content, &types.ContentBlockMemberImage{Value: types.ImageBlock{
			Format: types.ImageFormat(img.Format), Source: &types.ImageSourceMemberBytes{Value: img.Data},
		}})
	}
	if hasText(req.Text) {
		current.Content = append(current.Content, &types.ContentBlockMemberText{Value: req.Text})
	}
	input.Messages = append(input.Messages, current)
	return input
}

func (b *Bedrock) Send(ctx context.Context, req Request, onText func(string)) Result {
	var reply reply
	fail := func(err error) Result { return reply.result(ctx, "error", err) }
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if err := validateSend(req); err != nil {
		return fail(err)
	}
	if req.MaxOutput > math.MaxInt32 {
		return fail(errors.New("output token limit is too large for Bedrock"))
	}
	call, err := b.getClient(ctx)
	if err != nil {
		return fail(bedrockError(err, len(req.Images) > 0))
	}
	stream, err := call(ctx, bedrockInput(req))
	if err != nil {
		return fail(bedrockError(err, len(req.Images) > 0))
	}
	if stream == nil || stream.Reader == nil {
		return fail(errBedrockIncomplete)
	}
	defer stream.Close()
	var reason types.StopReason
	events := stream.Events()
	for {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		select {
		case <-ctx.Done():
			return fail(ctx.Err())
		case event, ok := <-events:
			if !ok {
				if err := stream.Err(); err != nil {
					return fail(bedrockError(err, len(req.Images) > 0))
				}
				stop, err := bedrockStop(reason)
				return reply.result(ctx, stop, err)
			}
			switch e := event.(type) {
			case *types.ConverseStreamOutputMemberContentBlockDelta:
				if delta, ok := e.Value.Delta.(*types.ContentBlockDeltaMemberText); ok {
					reply.append(delta.Value, onText)
				}
			case *types.ConverseStreamOutputMemberMetadata:
				if u := e.Value.Usage; u != nil {
					if usage := readUsage(u.InputTokens, u.OutputTokens, u.TotalTokens); usage != nil {
						reply.usage = usage
					}
				}
			case *types.ConverseStreamOutputMemberMessageStop:
				reason = e.Value.StopReason
			}
		}
	}
}

var (
	errBedrockIncomplete = errors.New("the Bedrock stream did not complete with a recognized stop reason")
	errBedrockUnknown    = errors.New("Bedrock request failed; check configuration, credentials, and network")
	errAWSRegion         = errors.New("AWS region is missing; set --region, AWS_REGION, or AWS_DEFAULT_REGION")
	errAWSReauth         = errors.New("AWS credentials are invalid or expired; for SSO, run aws sso login with the matching profile")
)

func bedrockStop(reason types.StopReason) (string, error) {
	switch reason {
	case types.StopReasonEndTurn, types.StopReasonStopSequence, types.StopReasonMaxTokens:
		return string(reason), nil
	case types.StopReasonToolUse:
		return string(reason), errToolUse
	case types.StopReasonGuardrailIntervened:
		return string(reason), errors.New("a Bedrock guardrail blocked the reply")
	case types.StopReasonContentFiltered:
		return string(reason), errors.New("a Bedrock content filter blocked the reply")
	case types.StopReasonMalformedModelOutput:
		return string(reason), errors.New("Bedrock returned malformed model output")
	case types.StopReasonMalformedToolUse:
		return string(reason), errors.New("the model returned a malformed tool call; tool calls are not supported")
	case types.StopReasonModelContextWindowExceeded:
		return string(reason), errors.New("the model's context window was exceeded; shorten the input")
	default:
		return "error", errBedrockIncomplete
	}
}

var errAWSNoCredentials = errors.New("no usable AWS credentials; check your credential configuration or run aws sso login for SSO")

func bedrockError(err error, vision bool) error {
	for _, safe := range []error{errAWSRegion, errAWSReauth, errAWSNoCredentials} {
		if errors.Is(err, safe) {
			return safe
		}
	}
	var profile config.SharedConfigProfileNotExistError
	if errors.As(err, &profile) {
		return errors.New("AWS profile not found; check AWS_DEFAULT_PROFILE and AWS_PROFILE or specify --profile <name>")
	}
	var api smithy.APIError
	var params smithy.InvalidParamsError
	code := ""
	if errors.As(err, &api) {
		code = strings.ToLower(api.ErrorCode())
	} else if errors.As(err, &params) {
		code = "validationexception"
	}
	switch code {
	case "expiredtoken", "expiredtokenexception", "invalidclienttokenid", "invalidsignatureexception", "unrecognizedclientexception", "unauthorizedexception":
		return errAWSReauth
	case "validationexception":
		if vision {
			return errors.New("Bedrock validation failed; check the model ID, region, and parameters, and confirm that the model supports vision and the images meet its limits")
		}
		return errors.New("Bedrock validation failed; check the model ID, region, and parameters")
	case "accessdeniedexception":
		return errors.New("Bedrock denied access; check IAM permissions and model access")
	case "internalserverexception":
		return errors.New("Bedrock internal service error")
	case "modelerrorexception":
		return errors.New("Bedrock failed to process the request; check the selected model")
	case "modelnotreadyexception":
		return errors.New("the Bedrock model is not ready")
	case "modelstreamerrorexception":
		return errors.New("Bedrock model stream interrupted")
	case "modeltimeoutexception":
		return errors.New("the Bedrock model timed out")
	case "resourcenotfoundexception":
		return errors.New("Bedrock model not found; check the model ID, inference profile, and region")
	case "servicequotaexceededexception":
		return errors.New("Bedrock service quota exceeded")
	case "serviceunavailableexception":
		return errors.New("the Bedrock service is temporarily unavailable")
	case "throttlingexception":
		return errors.New("the Bedrock request was throttled")
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return errors.New("connecting to or reading from Bedrock timed out")
	}
	var sendErr *smithyhttp.RequestSendError
	if errors.As(err, &sendErr) {
		return errors.New("Bedrock network connection failed; check your network and AWS region")
	}
	return errBedrockUnknown
}
