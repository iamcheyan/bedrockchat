package main

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

const (
	clearPreview     = "\r\x1b[2K"
	previewInterval  = 80 * time.Millisecond
	previewTailLimit = 4096
)

// replyPreview displays recent stream text on one replaceable row. The full
// reply is rendered separately, after the backend finishes, so a partial fence,
// table, or reference link never has to be interpreted as a complete document.
type replyPreview struct {
	out     io.Writer
	width   func() int
	tail    string
	last    time.Time
	visible bool
}

func (p *replyPreview) feed(chunk string) {
	// Limit both retained state and the cost of a large individual chunk.
	if len(chunk) >= previewTailLimit {
		p.tail = chunk[len(chunk)-previewTailLimit:]
	} else {
		p.tail += chunk
		if len(p.tail) > previewTailLimit {
			p.tail = p.tail[len(p.tail)-previewTailLimit:]
		}
	}
	now := time.Now()
	if now.Sub(p.last) < previewInterval {
		return
	}
	width := 80
	if p.width != nil {
		width = p.width()
	}
	text := previewLine(p.tail, width)
	if text == "" {
		return
	}
	fmt.Fprintf(p.out, "%s\x1b[90m%s\x1b[39m", clearPreview, text)
	p.visible = true
	p.last = now
}

func (p *replyPreview) clear() {
	if p.visible {
		fmt.Fprint(p.out, clearPreview)
	}
}

func previewLine(tail string, width int) string {
	// Leave one spare column to avoid triggering automatic line wrap.
	limit := width - 1
	if limit < 2 {
		return ""
	}
	for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
		tail = tail[1:]
	}
	text := strings.Join(strings.Fields(clean(tail)), " ")
	if uniseg.StringWidth(text) <= limit {
		return text
	}
	clusters := uniseg.NewGraphemes(text)
	remaining := uniseg.StringWidth(text)
	for clusters.Next() {
		remaining -= clusters.Width()
		if remaining <= limit-1 {
			_, end := clusters.Positions()
			return "…" + text[end:]
		}
	}
	return "…"
}

var sgr = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// Keep renderer colors and styling, but discard other terminal controls that
// could appear after Markdown entity decoding (e.g. an encoded ESC or BEL).
func safeMarkdownOutput(text string) string {
	var result strings.Builder
	start := 0
	for _, loc := range sgr.FindAllStringIndex(text, -1) {
		result.WriteString(clean(text[start:loc[0]]))
		result.WriteString(text[loc[0]:loc[1]])
		start = loc[1]
	}
	result.WriteString(clean(text[start:]))
	return result.String()
}
