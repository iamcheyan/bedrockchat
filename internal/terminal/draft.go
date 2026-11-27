package terminal

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

// One cell is a text grapheme or an atomic image/paste. A typed label cannot
// create an attachment. seq records insertion order, not position.
type cell struct {
	Part
	seq int
}

func (c cell) atomic() bool { return c.Image != nil || c.Paste != nil }

func pasteLines(text string) int {
	if text == "" {
		return 0
	}
	return strings.Count(strings.TrimSuffix(text, "\n"), "\n") + 1
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
		if p.Paste != nil {
			text := cleanText(*p.Paste)
			d.seq++
			d.cells = append(d.cells, cell{Part: Part{Paste: &text}, seq: d.seq})
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

func (d *draft) insertPaste(text string) {
	if strings.Contains(text, "\n") || utf8.RuneCountInString(text) > 1000 {
		d.seq++
		d.cells = slices.Insert(d.cells, d.pos, cell{Part: Part{Paste: &text}, seq: d.seq})
		d.pos++
		return
	}
	d.insertText(text)
}

func (d *draft) expandPaste() {
	index := d.pos - 1
	if index < 0 || d.cells[index].Paste == nil {
		index = d.pos
	}
	if index >= 0 && index < len(d.cells) && d.cells[index].Paste != nil {
		text := *d.cells[index].Paste
		d.remove(index)
		d.pos = index
		d.insertText(text)
	}
}

func (d *draft) insertText(text string) {
	if text == "" {
		return
	}
	// Re-segment the text around the splice, since e.g. an accent or a ZWJ
	// typed as a new key can join an existing grapheme across the cursor.
	l, r := d.pos, d.pos
	if l > 0 && !d.cells[l-1].atomic() {
		l--
		text = d.cells[l].Text + text
	}
	cursor := len(text)
	if r < len(d.cells) && !d.cells[r].atomic() {
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
		if pos > 0 && pos < len(d.cells) && !d.cells[pos-1].atomic() && !d.cells[pos].atomic() {
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
	// The text history already includes expanded pasted content. Preserve only
	// pending images, not a second copy of a folded paste.
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
		if !c.atomic() {
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
