package chat

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Thinking controls optional model reasoning. The zero value preserves the
// provider default; it does not mean that the model runs without thinking.
type Thinking string

const (
	ThinkingAuto Thinking = "auto"
	ThinkingOn   Thinking = "on"
	ThinkingOff  Thinking = "off"
)

func (m Thinking) Valid() bool {
	return m == ThinkingAuto || m == ThinkingOn || m == ThinkingOff
}

var (
	errThinkingMode        = errors.New("thinking must be auto, on, or off")
	errThinkingUnsupported = errors.New("this thinking mode is not supported for the selected Bedrock model; use /think auto or choose another model")
	claudeModel            = regexp.MustCompile(`^(?:(?:[a-z]{2}|apac|global)\.)?anthropic\.(claude-[a-z0-9-]+?)(?:-v[0-9]+)?(?::[0-9]+)?$`)
	claudeDatedModel       = regexp.MustCompile(`-[0-9]{8}$`)
)

// bedrockThinking maps documented Claude model families to their native
// request schema. Unknown IDs, including opaque application inference profiles,
// stay usable in auto mode without a provider-specific option.
func bedrockThinking(req Request) (map[string]any, error) {
	if req.Thinking == "" || req.Thinking == ThinkingAuto {
		return nil, nil
	}
	if !req.Thinking.Valid() {
		return nil, errThinkingMode
	}
	id := req.Model
	if strings.HasPrefix(id, "arn:") {
		i := strings.LastIndexByte(id, '/')
		if i == -1 {
			return nil, errThinkingUnsupported
		}
		id = id[i+1:]
	}
	matches := claudeModel.FindStringSubmatch(id)
	if matches == nil {
		return nil, errThinkingUnsupported
	}
	family := claudeDatedModel.ReplaceAllString(matches[1], "")
	thinking := map[string]any{}
	switch family {
	case "claude-haiku-4-5", "claude-sonnet-4-5", "claude-opus-4-5", "claude-sonnet-4", "claude-opus-4", "claude-3-7-sonnet":
		if req.Thinking == ThinkingOff {
			return nil, nil // Manual thinking on these models is opt-in.
		}
		const budget = 1024
		if req.MaxOutput <= budget {
			return nil, fmt.Errorf("this model needs --max-output-tokens greater than %d to enable thinking (or use /think off)", budget)
		}
		thinking["type"] = "enabled"
		thinking["budget_tokens"] = budget
	case "claude-opus-4-6", "claude-sonnet-4-6", "claude-opus-4-7", "claude-opus-4-8", "claude-opus-5", "claude-sonnet-5", "claude-haiku-5-5":
		thinking["type"] = "adaptive"
		if req.Thinking == ThinkingOff {
			thinking["type"] = "disabled"
		}
	case "claude-sonnet-5-5":
		thinking["type"] = "adaptive"
		if req.Thinking == ThinkingOff {
			// This model rejects disabled. No tools are supplied by this client.
			thinking["type"] = "between_tools"
		}
	case "claude-opus-5-5", "claude-fable-5", "claude-fable-5-1", "claude-fable-5-2", "claude-mythos-5", "claude-mythos-5-1", "claude-mythos-5-2":
		if req.Thinking == ThinkingOff {
			return nil, errThinkingUnsupported
		}
		thinking["type"] = "adaptive"
	default:
		return nil, errThinkingUnsupported
	}
	return map[string]any{"thinking": thinking}, nil
}
