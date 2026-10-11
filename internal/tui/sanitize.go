package tui

import (
	"strings"
	"unicode/utf8"
)

// sanitizeTerminalText removes terminal control sequences from untrusted text.
// Newlines and tabs are retained only for multiline detail content.
func sanitizeTerminalText(value string, multiline bool) string {
	var out strings.Builder
	for index := 0; index < len(value); {
		current := value[index]
		if current == 0x1b {
			index = skipEscapeSequence(value, index)
			continue
		}
		if current < 0x20 || current == 0x7f {
			switch current {
			case '\n', '\t':
				if multiline {
					out.WriteByte(current)
				} else {
					writeSpace(&out)
				}
			case '\r':
				writeSpace(&out)
			}
			index++
			continue
		}
		if current >= utf8.RuneSelf {
			r, size := utf8.DecodeRuneInString(value[index:])
			if r >= 0x80 && r <= 0x9f {
				index += size
				continue
			}
			out.WriteString(value[index : index+size])
			index += size
			continue
		}
		out.WriteByte(current)
		index++
	}
	return out.String()
}

func skipEscapeSequence(value string, start int) int {
	if start+1 >= len(value) {
		return len(value)
	}
	switch value[start+1] {
	case '[':
		for index := start + 2; index < len(value); index++ {
			if value[index] >= 0x40 && value[index] <= 0x7e {
				return index + 1
			}
		}
		return len(value)
	case ']':
		for index := start + 2; index < len(value); index++ {
			if value[index] == '\a' {
				return index + 1
			}
			if value[index] == 0x1b && index+1 < len(value) && value[index+1] == '\\' {
				return index + 2
			}
		}
		return len(value)
	default:
		return start + 2
	}
}

func writeSpace(out *strings.Builder) {
	if out.Len() == 0 {
		return
	}
	current := out.String()
	if current[len(current)-1] != ' ' {
		out.WriteByte(' ')
	}
}
