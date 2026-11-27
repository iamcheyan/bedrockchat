package terminal

import (
	"bufio"
	"errors"
	"io"
	"strings"

	"golang.org/x/sys/unix"
)

type key int

const (
	keyText key = iota + 256
	keyLeft
	keyRight
	keyUp
	keyDown
	keyHome
	keyEnd
	keyDelete
	keyNewline
	keyPasteTooLarge
	keyIgnore
)

type input struct {
	r   *bufio.Reader
	fd  int
	alt bool
}

func (in *input) next() (key, string, error) {
	r, _, err := in.r.ReadRune()
	if err != nil {
		return 0, "", err
	}
	alt := in.alt
	in.alt = false
	if r == '\x1b' {
		return in.escape()
	}
	if alt && (r == '\r' || r == '\n') {
		return keyNewline, "", nil
	}
	if r < 0x20 || r == 0x7f {
		return key(r), "", nil
	}
	return keyText, cleanText(string(r)), nil
}

func (in *input) escape() (key, string, error) {
	if !in.ready(60) {
		// Remember a standalone Esc so a later Enter also inserts a newline.
		in.alt = true
		return 0x1b, "", nil
	}
	c, err := in.r.ReadByte()
	if errors.Is(err, io.EOF) {
		return 0x1b, "", nil
	}
	if err != nil {
		return 0, "", err
	}
	switch c {
	case '\r', '\n':
		return keyNewline, "", nil
	case '[', 'O':
	default:
		_ = in.r.UnreadByte()
		return 0x1b, "", nil
	}
	var params strings.Builder
	for params.Len() < 32 && in.ready(150) {
		b, err := in.r.ReadByte()
		if err != nil {
			return 0, "", err
		}
		if b < 0x20 || b > 0x7e {
			_ = in.r.UnreadByte()
			break
		}
		if b < 0x40 {
			params.WriteByte(b)
			continue
		}
		var k key
		switch b {
		case 'A':
			k = keyUp
		case 'B':
			k = keyDown
		case 'C':
			k = keyRight
		case 'D':
			k = keyLeft
		case 'H':
			k = keyHome
		case 'F':
			k = keyEnd
		case 'u':
			if params.String() == "13;3" { // CSI-u Alt+Enter
				k = keyNewline
			}
		case '~':
			switch params.String() {
			case "1", "7":
				k = keyHome
			case "4", "8":
				k = keyEnd
			case "3":
				k = keyDelete
			case "200":
				return in.bracketedPaste()
			}
		}
		if k != 0 {
			return k, "", nil
		}
		break
	}
	return keyIgnore, "", nil
}

func (in *input) ready(ms int) bool {
	if in.r.Buffered() != 0 {
		return true
	}
	fds := []unix.PollFd{{Fd: int32(in.fd), Events: unix.POLLIN}}
	for {
		n, err := unix.Poll(fds, ms)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		return err == nil && n > 0
	}
}

func (in *input) bracketedPaste() (key, string, error) {
	const end = "\x1b[201~"
	const limit = 1024 * 1024
	var text strings.Builder
	matched := 0
	tooLarge := false
	for {
		b, err := in.r.ReadByte()
		if err != nil {
			return 0, "", err
		}
		if b == end[matched] {
			matched++
			if matched == len(end) {
				if tooLarge {
					return keyPasteTooLarge, "", nil
				}
				return keyText, cleanText(text.String()), nil
			}
			continue
		}
		if !tooLarge {
			if text.Len()+matched+1 > limit {
				tooLarge = true
			} else {
				text.WriteString(end[:matched])
				if b != end[0] {
					text.WriteByte(b)
				}
			}
		}
		matched = 0
		if b == end[0] {
			matched = 1
		}
	}
}
