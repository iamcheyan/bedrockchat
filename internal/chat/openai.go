package chat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

const (
	connectTimeout = 5 * time.Second
	ioTimeout      = 60 * time.Second
	maxResponse    = 8 << 20
)

var (
	errEndpoint       = errors.New("use a full /chat/completions endpoint without credentials, query or fragment; HTTP is permitted only on loopback")
	errOAReply        = errors.New("the OpenAI-compatible service returned an invalid reply")
	errOAIncomplete   = errors.New("the OpenAI-compatible reply did not complete with a recognized stop reason")
	errOALimit        = errors.New("the OpenAI-compatible reply exceeded the size limit")
	errOATimeout      = errors.New("connecting to or reading from the OpenAI-compatible service timed out")
	errOANetwork      = errors.New("OpenAI-compatible request failed; check the endpoint and network settings")
	errOAFilter       = errors.New("the OpenAI-compatible service's content filter blocked the reply")
	endpointAuthority = regexp.MustCompile(`^(\[[0-9a-fA-F:]+\]|[a-zA-Z0-9.-]+)(:[0-9]+)?$`)
)

type OpenAI struct {
	endpoint *url.URL
	client   *http.Client
	stream   bool
}

// NewOpenAI uses an explicit, unauthenticated Chat Completions endpoint.
// Invalid endpoints are reported as a safe error by Send, before any network I/O.
func NewOpenAI(endpoint string, stream bool) *OpenAI {
	u := parseEndpoint(endpoint)
	b := &OpenAI{endpoint: u, stream: stream}
	if u == nil {
		return b
	}
	dialer := &net.Dialer{Timeout: connectTimeout}
	if u.Scheme == "http" {
		// Also verify the address selected by the resolver for localhost.
		dialer.ControlContext = func(_ context.Context, _, addr string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(addr)
			if err != nil || !net.ParseIP(host).IsLoopback() {
				return errEndpoint
			}
			return nil
		}
	}
	b.client = &http.Client{
		Transport: &http.Transport{
			DialContext: timedDial(dialer), TLSHandshakeTimeout: connectTimeout,
			DisableKeepAlives: true, DisableCompression: true,
		},
		CheckRedirect: noRedirect,
	}
	return b
}

func parseEndpoint(raw string) *url.URL {
	for _, r := range raw {
		if r < '!' || r > '~' || strings.ContainsRune(`\?#`, r) {
			return nil
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || !endpointAuthority.MatchString(u.Host) || !strings.HasSuffix(u.EscapedPath(), "/chat/completions") {
		return nil
	}
	host := strings.ToLower(u.Hostname())
	if strings.HasPrefix(u.Host, "[") && net.ParseIP(host) == nil {
		return nil
	}
	if rawPort := u.Port(); rawPort != "" {
		if port, err := strconv.ParseUint(rawPort, 10, 16); err != nil || port == 0 {
			return nil
		}
	}
	if u.Scheme == "https" || (u.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1")) {
		return u
	}
	return nil
}

func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

type idleConn struct{ net.Conn }

func (c idleConn) Read(p []byte) (int, error) {
	if err := c.SetReadDeadline(time.Now().Add(ioTimeout)); err != nil {
		return 0, err
	}
	return c.Conn.Read(p)
}

func (c idleConn) Write(p []byte) (int, error) {
	if err := c.SetWriteDeadline(time.Now().Add(ioTimeout)); err != nil {
		return 0, err
	}
	return c.Conn.Write(p)
}

func timedDial(dialer *net.Dialer) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dialer.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return idleConn{conn}, nil
	}
}

type oaMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type oaPart struct {
	Type  string            `json:"type"`
	Text  string            `json:"text,omitempty"`
	Image map[string]string `json:"image_url,omitempty"`
}

type oaPayload struct {
	Model         string          `json:"model"`
	Messages      []oaMessage     `json:"messages"`
	MaxTokens     int             `json:"max_tokens"`
	Stream        bool            `json:"stream"`
	StreamOptions map[string]bool `json:"stream_options,omitempty"`
}

func openAIPayload(req Request, stream bool) (oaPayload, error) {
	p := oaPayload{Model: req.Model, MaxTokens: req.MaxOutput, Stream: stream}
	if stream {
		p.StreamOptions = map[string]bool{"include_usage": true}
	}
	if req.System != "" {
		p.Messages = append(p.Messages, oaMessage{"system", req.System})
	}
	for _, turn := range req.History {
		p.Messages = append(p.Messages, oaMessage{"user", turn.User}, oaMessage{"assistant", turn.Assistant})
	}
	var content any = req.Text
	if len(req.Images) != 0 {
		parts := make([]oaPart, 0, len(req.Images)+1)
		for _, img := range req.Images {
			if img.Format != "png" && img.Format != "jpeg" {
				return oaPayload{}, errors.New("the OpenAI-compatible backend accepts PNG and JPEG images only")
			}
			parts = append(parts, oaPart{Type: "image_url", Image: map[string]string{
				"url": "data:image/" + img.Format + ";base64," + base64.StdEncoding.EncodeToString(img.Data),
			}})
		}
		if hasText(req.Text) {
			parts = append(parts, oaPart{Type: "text", Text: req.Text})
		}
		content = parts
	}
	p.Messages = append(p.Messages, oaMessage{"user", content})
	return p, nil
}

func (b *OpenAI) Send(ctx context.Context, req Request, onText func(string)) Result {
	var reply reply
	fail := func(err error) Result { return reply.result(ctx, "error", err) }
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if b.endpoint == nil {
		return fail(errEndpoint)
	}
	if err := validateSend(req); err != nil {
		return fail(err)
	}
	payload, err := openAIPayload(req, b.stream)
	if err != nil {
		return fail(err)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fail(errOANetwork)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, b.endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return fail(errEndpoint)
	}
	request.GetBody = nil // the request must not be replayed
	request.Header.Set("Content-Type", "application/json")
	accept := "application/json"
	if b.stream {
		accept = "text/event-stream, " + accept
	}
	request.Header.Set("Accept", accept)
	response, err := b.client.Do(request)
	if err != nil {
		return fail(openAIIoError(err))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fail(fmt.Errorf("OpenAI-compatible request failed (HTTP %d)", response.StatusCode))
	}
	if response.ContentLength > maxResponse {
		return fail(errOALimit)
	}
	mediaType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	isSSE := mediaType == "text/event-stream"
	var reason string
	consume := func(raw []byte) error { return readOpenAIEvent(raw, isSSE, &reply, &reason, onText) }
	reader := &io.LimitedReader{R: response.Body, N: maxResponse + 1}
	if isSSE {
		err = readOpenAISSE(ctx, reader, consume)
	} else {
		var raw []byte
		raw, err = io.ReadAll(reader)
		switch {
		case reader.N == 0:
			err = errOALimit
		case err != nil:
			err = openAIIoError(err)
		default:
			err = consume(raw)
		}
	}
	if err != nil {
		return fail(err)
	}
	switch reason {
	case "stop":
		return reply.result(ctx, "end_turn", nil)
	case "length":
		return reply.result(ctx, "max_tokens", nil)
	case "tool_calls", "function_call":
		return reply.result(ctx, "tool_use", errToolUse)
	case "content_filter":
		return reply.result(ctx, "content_filtered", errOAFilter)
	default:
		return fail(errOAIncomplete)
	}
}

func openAIIoError(err error) error {
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return errOATimeout
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return errOAIncomplete
	}
	return errOANetwork
}

func readOpenAISSE(ctx context.Context, reader *io.LimitedReader, consume func([]byte) error) error {
	input := bufio.NewReader(reader)
	var event []byte
	first := true
	var remaining int64 = maxResponse
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := input.ReadString('\n')
		remaining -= int64(len(line))
		if remaining < 0 {
			return errOALimit
		}
		if err != nil {
			return openAIIoError(err)
		}
		if first {
			line = strings.TrimPrefix(line, "\xef\xbb\xbf")
			first = false
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if line == "" {
			if len(event) == 0 {
				continue
			}
			data := event[:len(event)-1]
			if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
				return nil
			}
			if err := consume(data); err != nil {
				return err
			}
			event = event[:0]
			continue
		}
		if field, value, _ := strings.Cut(line, ":"); field == "data" {
			event = append(event, strings.TrimPrefix(value, " ")...)
			event = append(event, '\n')
		}
	}
}

func readOpenAIEvent(raw []byte, isSSE bool, reply *reply, reason *string, onText func(string)) error {
	var event map[string]json.RawMessage
	if !utf8.Valid(raw) || json.Unmarshal(raw, &event) != nil || event == nil || event["error"] != nil {
		return errOAReply
	}
	if rawUsage := event["usage"]; rawUsage != nil {
		var u struct {
			Input  *int `json:"prompt_tokens"`
			Output *int `json:"completion_tokens"`
			Total  *int `json:"total_tokens"`
		}
		if json.Unmarshal(rawUsage, &u) == nil {
			if usage := readUsage(u.Input, u.Output, u.Total); usage != nil {
				reply.usage = usage
			}
		}
	}
	if event["choices"] == nil {
		return nil
	}
	var choices []json.RawMessage
	if json.Unmarshal(event["choices"], &choices) != nil || choices == nil {
		return errOAReply
	}
	if len(choices) == 0 {
		return nil
	}
	var choice map[string]json.RawMessage
	if json.Unmarshal(choices[0], &choice) != nil || choice == nil {
		return errOAReply
	}
	key := "message"
	if isSSE {
		key = "delta"
	}
	if rawContent := choice[key]; rawContent != nil || !isSSE {
		var content map[string]json.RawMessage
		if json.Unmarshal(rawContent, &content) != nil || content == nil {
			return errOAReply
		}
		var text string
		if json.Unmarshal(content["content"], &text) == nil {
			reply.append(text, onText)
		}
	}
	if rawReason := choice["finish_reason"]; rawReason != nil && !bytes.Equal(rawReason, []byte("null")) {
		*reason = ""
		_ = json.Unmarshal(rawReason, reason)
	}
	return nil
}
