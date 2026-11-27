package main

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/x/ansi"
)

const (
	clearPreview    = "\r\x1b[2K"
	previewInterval = 80 * time.Millisecond
)

func newReplyRenderer(width int) (*glamour.TermRenderer, error) {
	style := styles.DarkStyleConfig
	for _, prefix := range []*string{&style.H2.Prefix, &style.H3.Prefix, &style.H4.Prefix, &style.H5.Prefix, &style.H6.Prefix} {
		*prefix = ""
	}
	return glamour.NewTermRenderer(glamour.WithStyles(style), glamour.WithWordWrap(max(1, min(width-1, 100))))
}

// Reparse the accumulated Markdown so newly completed tables and fences can
// change how earlier text looks. Only a screenful is repainted; the final
// render writes the whole reply to normal terminal scrollback.
type replyPreview struct {
	out    io.Writer
	size   func() (int, int)
	render func(string) string
	text   strings.Builder
	last   time.Time
	rows   int
	color  bool
}

func (p *replyPreview) feed(chunk string) {
	if chunk == "" {
		return
	}
	p.text.WriteString(chunk)
	now := time.Now()
	if now.Sub(p.last) < previewInterval {
		return
	}
	width, height := 80, 24
	if p.size != nil {
		width, height = p.size()
	}
	rows := previewRows(p.render(p.text.String()), width, height)
	p.clear()
	if len(rows) == 0 {
		return
	}
	reset := ""
	if p.color {
		reset = "\x1b[0m"
	}
	fmt.Fprint(p.out, strings.Join(rows, "\r\n"), reset)
	p.rows, p.last = len(rows), now
}

func (p *replyPreview) clear() {
	var frame strings.Builder
	for row := p.rows; row > 0; row-- {
		frame.WriteString(clearPreview)
		if row > 1 {
			frame.WriteString("\x1b[1A")
		}
	}
	if p.rows > 0 {
		io.WriteString(p.out, frame.String())
	}
	p.rows = 0
}

func previewRows(text string, width, height int) []string {
	// Leave a spare row and column so the cursor never wraps implicitly.
	if width < 3 || height < 2 || strings.TrimSpace(ansi.Strip(text)) == "" {
		return nil
	}
	text = strings.Trim(text, "\n")
	text = strings.ReplaceAll(text, "\t", "    ")
	rows := strings.Split(ansi.Hardwrap(text, width-1, true), "\n")
	return rows[max(0, len(rows)-height+1):]
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
