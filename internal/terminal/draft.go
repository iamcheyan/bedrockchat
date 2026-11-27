package terminal

import (
	"slices"
	"strings"
	"unicode"

	"github.com/rivo/uniseg"
)

// One cell is either a text grapheme or an image. A typed or pasted label can
// never turn into an attachment. seq records attachment order, not position.
type cell struct {
	Part
	seq int
}

type draft struct {
	cells []cell
	pos   int
	seq   int
}

func newDraft(parts []Part) draft {
	var d draft
	for _, p := range parts {
		d.insertText(cleanText(p.Text))
		if p.Image != nil {
			d.seq++
			d.cells = append(d.cells, cell{Part: Part{Image: p.Image}, seq: d.seq})
			d.pos++
		}
	}
	return d
}

func cleanText(text string) string {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	return strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && unicode.IsControl(r) {
			return -1
		}
		return r
	}, text)
}

func cellsFor(text string) []cell {
	var cells []cell
	g := uniseg.NewGraphemes(text)
	for g.Next() {
		cells = append(cells, cell{Part: Part{Text: g.Str()}})
	}
	return cells
}

func (d *draft) insertText(text string) {
	if text == "" {
		return
	}
	// Re-segment the text around the splice, since e.g. an accent or a ZWJ
	// typed as a new key can join an existing grapheme across the cursor.
	l, r := d.pos, d.pos
	if l > 0 && d.cells[l-1].Image == nil {
		l--
		text = d.cells[l].Text + text
	}
	cursor := len(text)
	if r < len(d.cells) && d.cells[r].Image == nil {
		text += d.cells[r].Text
		r++
	}
	cells := cellsFor(text)
	d.pos = l
	for _, c := range cells {
		if cursor <= 0 {
			break
		}
		d.pos++
		cursor -= len(c.Text)
	}
	d.cells = slices.Replace(d.cells, l, r, cells...)
}

func (d *draft) remove(pos int) {
	if pos >= 0 && pos < len(d.cells) {
		d.cells = slices.Delete(d.cells, pos, pos+1)
		if d.pos > pos {
			d.pos--
		}
		if pos > 0 && pos < len(d.cells) && d.cells[pos-1].Image == nil && d.cells[pos].Image == nil {
			if text := d.cells[pos-1].Text + d.cells[pos].Text; uniseg.GraphemeClusterCount(text) == 1 {
				d.cells[pos-1].Text = text
				d.cells = slices.Delete(d.cells, pos, pos+1)
				if d.pos > pos {
					d.pos--
				}
			}
		}
	}
}

func (d *draft) dropNewest() {
	newest, seq := -1, 0
	for i, c := range d.cells {
		if c.seq > seq {
			newest, seq = i, c.seq
		}
	}
	d.remove(newest)
}

func (d *draft) recall(text string) {
	// Recall is text-only: preserve pending images (including their paste order).
	d.cells = append(cellsFor(text), slices.DeleteFunc(d.cells, func(c cell) bool { return c.Image == nil })...)
	d.pos = len(d.cells)
	for d.pos > 0 && d.cells[d.pos-1].Image != nil {
		d.pos--
	}
}

func (d *draft) parts() []Part {
	var result []Part
	var text strings.Builder
	for _, c := range d.cells {
		if c.Image == nil {
			text.WriteString(c.Text)
			continue
		}
		if text.Len() > 0 {
			result = append(result, Part{Text: text.String()})
			text.Reset()
		}
		result = append(result, c.Part)
	}
	if text.Len() > 0 {
		result = append(result, Part{Text: text.String()})
	}
	return result
}

func (d *draft) lineStart() {
	for d.pos > 0 && d.cells[d.pos-1].Text != "\n" {
		d.pos--
	}
}

func (d *draft) lineEnd() {
	for d.pos < len(d.cells) && d.cells[d.pos].Text != "\n" {
		d.pos++
	}
}
