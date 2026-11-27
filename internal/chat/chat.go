// Package chat budgets text and inline images and sends chat-only requests.
package chat

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

// Image is a prepared inline attachment. Only Data and Format go on the wire.
type Image struct {
	Data          []byte
	Format        string
	Width, Height int
	Name          string
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// Pair is a complete user/assistant turn. History is ordered oldest first.
type Pair struct{ User, Assistant string }

type Request struct {
	Model, Text, System string
	History             []Pair
	Images              []Image
	MaxOutput           int
	Thinking            Thinking
}

// Result retains text and any valid usage even if the request fails.
type Result struct {
	Text       string
	Usage      *Usage
	StopReason string
	Err        error
}

// Backend sends a request synchronously, calling onText (if non-nil) for each
// nonempty text chunk. Callers should Trim first to apply their input budget.
type Backend interface {
	Send(context.Context, Request, func(string)) Result
}

var (
	modelID          = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.:_/-]*$`)
	errModel         = errors.New("invalid model ID or ARN")
	errManagedPrompt = errors.New("Prompt Management ARNs are not supported; use a model ID or inference profile ID/ARN")
	errLimits        = errors.New("input and output token limits must be positive integers")
	errEmpty         = errors.New("enter a message or attach an image")
	errImageSize     = errors.New("image dimensions must be positive integers")
	errImage         = errors.New("images must contain inline bytes in a supported format")
	errHistory       = errors.New("each history turn must contain user and assistant text")
	errToolUse       = errors.New("the model requested a tool call; tool calls are not supported")
)

func hasText(s string) bool { return strings.TrimSpace(s) != "" }

func validTurn(turn Pair) bool { return hasText(turn.User) && hasText(turn.Assistant) }

func validateCurrent(req Request) error {
	if req.Thinking != "" && !req.Thinking.Valid() {
		return errThinkingMode
	}
	if len(req.Model) > 2048 || !modelID.MatchString(req.Model) {
		return errModel
	}
	if strings.Contains(req.Model, ":prompt/") || strings.Contains(req.Model, ":prompt-version/") {
		return errManagedPrompt
	}
	if req.MaxOutput < 1 {
		return errLimits
	}
	if !hasText(req.Text) && len(req.Images) == 0 {
		return errEmpty
	}
	for _, img := range req.Images {
		if img.Width < 1 || img.Height < 1 {
			return errImageSize
		}
		if len(img.Data) == 0 {
			return errImage
		}
		switch img.Format {
		case "png", "jpeg", "gif", "webp":
		default:
			return errImage
		}
	}
	return nil
}

func validateSend(req Request) error {
	if err := validateCurrent(req); err != nil {
		return err
	}
	for _, turn := range req.History {
		if !validTurn(turn) {
			return errHistory
		}
	}
	return nil
}

type reply struct {
	text  strings.Builder
	usage *Usage
}

func (r *reply) append(chunk string, onText func(string)) {
	if chunk == "" {
		return
	}
	r.text.WriteString(chunk)
	if onText != nil {
		onText(chunk)
	}
}

func (r *reply) result(ctx context.Context, stop string, err error) Result {
	// Don't wrap arbitrary context causes: they may contain private data.
	if ctx.Err() != nil {
		stop, err = "interrupted", ctx.Err()
	}
	return Result{Text: r.text.String(), Usage: r.usage, StopReason: stop, Err: err}
}

func readUsage[T ~int | ~int32](input, output, total *T) *Usage {
	if input == nil || output == nil || total == nil || *input < 0 || *output < 0 || *total < 0 {
		return nil
	}
	return &Usage{InputTokens: int(*input), OutputTokens: int(*output), TotalTokens: int(*total)}
}
