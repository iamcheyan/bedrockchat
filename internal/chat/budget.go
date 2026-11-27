package chat

import (
	"fmt"
	"math/bits"

	"golang.org/x/text/width"
)

// Trim estimates input tokens and keeps the newest suffix of complete turns
// that fits. The current message (including images) and system are never
// truncated. The returned request shares the original slice data.
// On error it returns a zero request and zero counts.
func Trim(req Request, maxInput int) (Request, int, int, error) {
	if err := validateCurrent(req); err != nil {
		return Request{}, 0, 0, err
	}
	if maxInput < 1 {
		return Request{}, 0, 0, errLimits
	}
	total := uint64(3 + 8)
	if hasText(req.Text) {
		total += textTokens(req.Text)
	}
	if req.System != "" {
		if total <= uint64(maxInput) {
			total += 8 + textTokens(req.System)
		}
	}
	over := func() (Request, int, int, error) {
		return Request{}, 0, 0, fmt.Errorf("current message (including images and system prompt) exceeds the input token budget of %d", maxInput)
	}
	if total > uint64(maxInput) {
		return over()
	}
	for _, img := range req.Images {
		// Use the full product before dividing; dimensions are caller-supplied.
		hi, lo := bits.Mul64(uint64(img.Width), uint64(img.Height))
		if hi >= 750 {
			return over()
		}
		cost, rem := bits.Div64(hi, lo, 750)
		left := uint64(maxInput) - total
		if cost > left || (cost == left && rem != 0) {
			return over()
		}
		total += cost
		if rem != 0 {
			total++
		}
	}

	dropped := len(req.History)
	for dropped > 0 {
		turn := req.History[dropped-1]
		if !validTurn(turn) {
			return Request{}, 0, 0, errHistory
		}
		left := uint64(maxInput) - total
		cost := 16 + textTokens(turn.User)
		if cost > left {
			break
		}
		cost += textTokens(turn.Assistant)
		if cost > left {
			break
		}
		total += cost
		dropped--
	}
	req.History = req.History[dropped:]
	return req, int(total), dropped, nil
}

func textTokens(text string) uint64 {
	var ascii, tokens uint64
	for _, r := range text {
		if r < 128 {
			ascii++
			continue
		}
		tokens++
		switch width.LookupRune(r).Kind() {
		case width.EastAsianWide, width.EastAsianFullwidth:
			tokens++
		}
	}
	return tokens + (ascii+2)/3
}
