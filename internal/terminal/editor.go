// Package terminal provides a reusable multiline chat draft editor for Unix terminals.
package terminal

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/iamcheyan/bedrockchat/internal/chat"
	"golang.org/x/term"
)

// ErrInterrupted is returned when Ctrl+C cancels the draft.
var ErrInterrupted = errors.New("input interrupted")

// Part is text or an atomic image. Read returns parts in draft order, with
// adjacent text coalesced and without display labels. If an initial part has
// both fields, its text precedes its image.
type Part struct {
	Text  string
	Image *chat.Image
}

// Editor owns input buffering and in-memory text history between reads. Use it
// serially, and do not read from its input elsewhere while editing.
type Editor struct {
	// ImageLabel optionally supplies a plain-text label for an attachment. It
	// is for display only; nil or an empty result uses the default label.
	ImageLabel func(chat.Image) string

	in      *os.File
	out     io.Writer
	input   input
	history []string
}

// New creates an editor without changing terminal mode or reading input.
func New(in *os.File, out io.Writer) *Editor {
	return &Editor{in: in, out: out, input: input{r: bufio.NewReader(in)}}
}

// Remember adds text to this session's history. Call it only for input the
// caller wants to recall; Read never records drafts or attachments itself.
func (e *Editor) Remember(text string) {
	text = cleanText(text)
	if strings.TrimSpace(text) == "" || (len(e.history) > 0 && e.history[len(e.history)-1] == text) {
		return
	}
	const limit = 500
	if len(e.history) == limit {
		copy(e.history, e.history[1:])
		e.history[len(e.history)-1] = text
	} else {
		e.history = append(e.history, text)
	}
}

// Read edits initial until Enter, Ctrl+C (ErrInterrupted), or EOF. Esc then
// Enter inserts a newline. Ctrl+G calls paste synchronously, only on request.
// The optional onError callback reports paste errors above the redrawn draft;
// if it writes to the terminal, it should end its message with a newline.
// Read restores terminal mode before returning. The caller owns any later
// streaming and its signal handling.
func (e *Editor) Read(initial []Part, paste func() (chat.Image, error), onError func(error)) (parts []Part, err error) {
	fd := int(e.in.Fd())
	e.input.fd = fd
	e.input.alt = false
	interactive := term.IsTerminal(fd)
	s := screen{out: e.out, fd: fd}
	if interactive {
		state, err := term.MakeRaw(fd)
		if err != nil {
			return nil, err
		}
		defer func() {
			_, disableErr := io.WriteString(e.out, "\x1b[?2004l")
			restoreErr := term.Restore(fd, state)
			if restoreErr != nil || disableErr != nil {
				err = errors.Join(err, restoreErr, disableErr)
			}
		}()
		if _, err := io.WriteString(e.out, "\x1b[?2004h"); err != nil {
			return nil, err
		}
	} else if _, err := io.WriteString(e.out, prompt); err != nil {
		return nil, err
	}

	label := func(c cell) string {
		if e.ImageLabel != nil {
			if text := singleLine(e.ImageLabel(*c.Image)); text != "" {
				return text
			}
		}
		return fmt.Sprintf("[Image #%d]", c.seq)
	}
	d := newDraft(initial)
	h := len(e.history)
	var saved draft
	var search *historySearch
	chord := false
	report := func(err error) error {
		if onError == nil {
			return nil
		}
		if interactive {
			if err := s.clear(); err != nil {
				return err
			}
		}
		onError(err)
		return nil
	}
	for {
		if interactive {
			lead, view, pos := prompt, d.cells, d.pos
			if search != nil {
				lead = prompt + "(reverse search: " + singleLine(search.query) + ") "
				if search.match >= 0 {
					view = cellsFor(e.history[search.match])
					pos = len(view)
				}
			}
			if err := s.redraw(lead, view, pos, label); err != nil {
				return nil, err
			}
		}
		k, text, err := e.input.next()
		if err != nil {
			if interactive {
				err = errors.Join(err, s.finish())
			}
			return nil, err
		}
		if k == 3 { // Ctrl+C
			if interactive {
				return nil, errors.Join(ErrInterrupted, s.finish())
			}
			return nil, ErrInterrupted
		}
		if search != nil {
			switch k {
			case keyText, 8, 127, 18:
				search.update(k, text, e.history)
				continue
			case 0x1b, 7: // Esc or Ctrl+G cancels search without changing the draft.
				search = nil
				e.input.alt = false
				continue
			default:
				if search.match >= 0 {
					d.recall(e.history[search.match])
				}
				search = nil
				h = len(e.history)
				if k == '\r' || k == '\n' {
					continue // Accept the match for editing; a second Enter submits.
				}
			}
		}
		if chord && k == 4 {
			chord = false
			d.dropNewest()
			h = len(e.history)
			continue
		}
		chord = k == 24 // Ctrl+X
		if k != keyUp && k != keyDown && k != keyIgnore && k != 0x1b {
			h = len(e.history)
		}
		switch k {
		case '\r', '\n':
			if interactive {
				err = s.finish()
			} else {
				var b strings.Builder
				for _, c := range d.cells {
					if c.Image == nil {
						b.WriteString(c.Text)
					} else {
						b.WriteString(label(c))
					}
				}
				b.WriteByte('\n')
				_, err = io.WriteString(e.out, b.String())
			}
			return d.parts(), err
		case keyText:
			d.insertText(text)
		case '\t':
			d.insertText("\t")
		case keyNewline:
			d.insertText("\n")
		case keyLeft, 2:
			d.pos = max(0, d.pos-1)
		case keyRight, 6:
			d.pos = min(len(d.cells), d.pos+1)
		case keyHome, 1:
			d.lineStart()
		case keyEnd, 5:
			d.lineEnd()
		case 8, 127:
			d.remove(d.pos - 1)
		case 4: // Ctrl+D deletes forward, or exits at an empty prompt.
			if len(d.cells) == 0 {
				if interactive {
					return nil, errors.Join(io.EOF, s.finish())
				}
				return nil, io.EOF
			}
			d.remove(d.pos)
		case keyDelete:
			d.remove(d.pos)
		case keyUp:
			if h > 0 {
				if h == len(e.history) {
					saved = d
					saved.cells = slices.Clone(d.cells)
				}
				h--
				d.recall(e.history[h])
			}
		case keyDown:
			if h < len(e.history) {
				h++
				if h == len(e.history) {
					d = saved
				} else {
					d.recall(e.history[h])
				}
			}
		case 18: // Ctrl+R
			search = &historySearch{match: len(e.history) - 1}
		case keyPasteTooLarge:
			if err := report(errors.New("text paste exceeds 1 MiB")); err != nil {
				return nil, err
			}
		case 7: // Ctrl+G
			if paste != nil {
				img, err := paste()
				if err != nil {
					if err := report(err); err != nil {
						return nil, err
					}
				} else {
					d.seq++
					d.cells = slices.Insert(d.cells, d.pos, cell{Part: Part{Image: &img}, seq: d.seq})
					d.pos++
				}
			}
		}
	}
}

type historySearch struct {
	query string
	match int
}

func (s *historySearch) update(k key, text string, history []string) {
	from := len(history) - 1
	switch k {
	case keyText:
		s.query += text
	case 8, 127:
		if c := cellsFor(s.query); len(c) > 0 {
			s.query = s.query[:len(s.query)-len(c[len(c)-1].Text)]
		}
	case 18:
		from = s.match - 1
	}
	for i := from; i >= 0; i-- {
		if strings.Contains(history[i], s.query) {
			s.match = i
			return
		}
	}
	if k != 18 {
		s.match = -1
	}
}
