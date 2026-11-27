package terminal

import (
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/rivo/uniseg"
	"golang.org/x/term"
)

const prompt = "› "

type screen struct {
	out  io.Writer
	fd   int
	rows int
	row  int
}

func (s *screen) redraw(lead string, cells []cell, cursor int, label func(cell) string) error {
	width, height, err := term.GetSize(s.fd)
	if err != nil || width == 0 || height == 0 {
		width, height = 80, 24
	}
	rows, row, col := layout(lead, cells, cursor, width, label)
	// Keep the editing cursor visible when the draft exceeds a pane's height.
	start := max(0, row-height+1)
	rows, row = rows[start:min(start+height, len(rows))], row-start
	var b strings.Builder
	b.WriteByte('\r')
	moveUp(&b, min(s.row, height-1))
	for i := range max(len(rows), min(s.rows, height)) {
		if i > 0 {
			b.WriteString("\r\n")
		}
		b.WriteString("\x1b[2K")
		if i < len(rows) {
			b.WriteString(rows[i])
		}
	}
	moveUp(&b, max(len(rows), min(s.rows, height))-1-row)
	b.WriteByte('\r')
	if col > 0 {
		fmt.Fprintf(&b, "\x1b[%dC", col)
	}
	_, err = io.WriteString(s.out, b.String())
	s.rows, s.row = len(rows), row
	return err
}

func (s *screen) clear() error {
	if s.rows == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteByte('\r')
	moveUp(&b, s.row)
	for i := range s.rows {
		if i > 0 {
			b.WriteString("\r\n")
		}
		b.WriteString("\x1b[2K")
	}
	moveUp(&b, s.rows-1)
	s.rows, s.row = 0, 0
	_, err := io.WriteString(s.out, b.String())
	return err
}

func (s *screen) finish() error {
	var b strings.Builder
	if down := s.rows - s.row - 1; down > 0 {
		fmt.Fprintf(&b, "\x1b[%dB", down)
	}
	b.WriteString("\r\n")
	s.rows, s.row = 0, 0
	_, err := io.WriteString(s.out, b.String())
	return err
}

func moveUp(w io.Writer, count int) {
	if count > 0 {
		fmt.Fprintf(w, "\x1b[%dA", count)
	}
}

func layout(lead string, cells []cell, cursor, width int, label func(cell) string) (rows []string, row, col int) {
	var line strings.Builder
	x := 0
	newline := func() {
		rows = append(rows, line.String())
		line.Reset()
		x = 0
	}
	write := func(text string) {
		g := uniseg.NewGraphemes(text)
		for g.Next() {
			str, w := g.Str(), g.Width()
			switch str {
			case "\n":
				newline()
				continue
			case "\t":
				w = min(width, 4-x%4)
				str = strings.Repeat(" ", w)
			}
			if w > width {
				str, w = "?", 1
			}
			if x+w > width {
				newline()
			}
			line.WriteString(str)
			x += w
		}
	}
	write(lead)
	for i := 0; i <= len(cells); i++ {
		if i == cursor {
			if x == width && (i == len(cells) || cells[i].Text != "\n") {
				newline()
			}
			row, col = len(rows), min(x, width-1)
		}
		if i == len(cells) {
			break
		}
		c := cells[i]
		if c.Image == nil {
			write(c.Text)
		} else {
			write(label(c))
		}
	}
	return append(rows, line.String()), row, col
}

func singleLine(text string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\r' || r == '\n' || r == '\t':
			return ' '
		case unicode.IsControl(r):
			return -1
		default:
			return r
		}
	}, text)
}
