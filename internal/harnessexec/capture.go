package harnessexec

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"unicode"
)

// capture keeps the beginning and the end of a stream and counts what it dropped. A
// failing test usually explains itself in its last lines and a build in its first, so
// both ends are kept, with the larger share going to the end.
type capture struct {
	mu      sync.Mutex
	headCap int
	tailCap int
	head    []byte
	tail    []byte // ring of the most recent tailCap bytes, in order
	total   int64
}

func newCapture(max int) *capture {
	head := max / 3
	return &capture{headCap: head, tailCap: max - head}
}

func (c *capture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(p)
	c.total += int64(n)
	if room := c.headCap - len(c.head); room > 0 {
		take := min(room, len(p))
		c.head = append(c.head, p[:take]...)
		p = p[take:]
	}
	if len(p) == 0 {
		return n, nil
	}
	if len(p) >= c.tailCap {
		c.tail = append(c.tail[:0], p[len(p)-c.tailCap:]...)
		return n, nil
	}
	c.tail = append(c.tail, p...)
	if over := len(c.tail) - c.tailCap; over > 0 {
		c.tail = append(c.tail[:0], c.tail[over:]...)
	}
	return n, nil
}

// result returns what was kept and how many bytes were dropped between the two ends.
func (c *capture) result() (kept []byte, omitted int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	omitted = c.total - int64(len(c.head)) - int64(len(c.tail))
	kept = append(append([]byte(nil), c.head...), c.tail...)
	if omitted > 0 {
		marker := fmt.Sprintf("\n…[%d bytes omitted]…\n", omitted)
		kept = append(append(append([]byte(nil), c.head...), marker...), c.tail...)
	}
	return kept, omitted
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-Z\\-_]`)

// sanitize makes process output safe to keep and to show: terminal escape sequences are
// dropped whole, invalid UTF-8 and every other control character are removed, and carriage
// returns, which programs use to redraw a line, are dropped so a progress bar does not
// flood the result.
func sanitize(raw []byte) string {
	text := ansiRE.ReplaceAllString(strings.ToValidUTF8(string(raw), ""), "")
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r == '\r', unicode.IsControl(r), r == unicode.ReplacementChar:
		case r >= 0x200B && r <= 0x200F, r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069, r == 0xFEFF:
			fmt.Fprintf(&b, "⟨U+%04X⟩", r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
