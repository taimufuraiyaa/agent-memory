package harnesstools

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
)

const (
	previewContext = 3
	// largeChangeLines is the number of changed lines at which a change asks with extra
	// friction: a person reviewing hundreds of lines is the likeliest to miss one.
	largeChangeLines = 200
	// deletePreviewLines is how much of a deleted file the preview shows.
	deletePreviewLines = 40
)

// visible makes characters a reviewer could not otherwise see impossible to miss: control
// characters, bidirectional overrides that can reorder displayed code, zero-width and
// joiner characters, and byte-order marks each appear as a code-point marker. Newline and
// tab are kept. The preview is what a person approves, so it must not hide anything.
func visible(text string) string {
	text = strings.ToValidUTF8(text, "�")
	var b strings.Builder
	for _, r := range text {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case unicode.IsControl(r), r >= 0x200B && r <= 0x200F, r >= 0x202A && r <= 0x202E, r >= 0x2060 && r <= 0x2064,
			r >= 0x2066 && r <= 0x2069, r == 0xFEFF, r == 0x00AD, r == 0x061C, r == 0x180E:
			fmt.Fprintf(&b, "⟨U+%04X⟩", r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// span is one exact replacement in the original text.
type span struct {
	start, end int
	text       string
}

// block is a run of whole original lines and what they become.
type block struct {
	first    int // 0-based index of the first differing line in the original
	oldLines []string
	newLines []string
}

// splitKeep splits text into lines that keep their newline, so a missing final newline is
// visible in the diff.
func splitKeep(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.SplitAfter(text, "\n")
	if lines[len(lines)-1] == "" { // the text ended with a newline: no extra empty line
		lines = lines[:len(lines)-1]
	}
	return lines
}

func lineIndex(offsets []int, pos int) int {
	// offsets[i] is the byte offset where line i starts; find the line containing pos.
	return sort.Search(len(offsets), func(i int) bool { return offsets[i] > pos }) - 1
}

func lineOffsets(lines []string) []int {
	offsets := make([]int, len(lines)+1)
	for i, l := range lines {
		offsets[i+1] = offsets[i] + len(l)
	}
	return offsets[:len(lines)]
}

// blocks groups replacements into runs of whole lines. Two replacements that touch the
// same line become one block, and the unchanged lines at the start and end of a block are
// trimmed, so a block holds exactly the lines that differ.
func blocks(original string, spans []span) []block {
	lines := splitKeep(original)
	offsets := lineOffsets(lines)
	var out []block
	for i := 0; i < len(spans); {
		first := lineIndex(offsets, spans[i].start)
		last := lineIndex(offsets, max(spans[i].end-1, spans[i].start))
		j := i + 1
		for j < len(spans) && lineIndex(offsets, spans[j].start) <= last {
			last = max(last, lineIndex(offsets, max(spans[j].end-1, spans[j].start)))
			j++
		}
		from := offsets[first]
		to := len(original)
		if last+1 < len(lines) {
			to = offsets[last+1]
		}
		var next strings.Builder
		cursor := from
		for _, sp := range spans[i:j] {
			next.WriteString(original[cursor:sp.start])
			next.WriteString(sp.text)
			cursor = sp.end
		}
		next.WriteString(original[cursor:to])
		oldLines, newLines := splitKeep(original[from:to]), splitKeep(next.String())
		lead := 0
		for lead < len(oldLines) && lead < len(newLines) && oldLines[lead] == newLines[lead] {
			lead++
		}
		trail := 0
		for trail < len(oldLines)-lead && trail < len(newLines)-lead && oldLines[len(oldLines)-1-trail] == newLines[len(newLines)-1-trail] {
			trail++
		}
		if len(oldLines)-lead-trail > 0 || len(newLines)-lead-trail > 0 {
			out = append(out, block{first: first + lead, oldLines: oldLines[lead : len(oldLines)-trail], newLines: newLines[lead : len(newLines)-trail]})
		}
		i = j
	}
	return out
}

func writeLine(b *strings.Builder, mark byte, line string) {
	b.WriteByte(mark)
	b.WriteString(visible(strings.TrimSuffix(line, "\n")))
	b.WriteByte('\n')
	if !strings.HasSuffix(line, "\n") {
		b.WriteString("\\ No newline at end of file\n")
	}
}

// editPreview renders a unified diff of the replacements, built from the ranges rather
// than from a general diff, so it is exactly the change that will be written. It returns
// the preview and the number of lines that change.
func editPreview(path, original string, spans []span) (string, int) {
	lines := splitKeep(original)
	changed := blocks(original, spans)
	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", visible(path), visible(path))
	delta, total := 0, 0
	for i := 0; i < len(changed); {
		// Merge blocks whose context windows touch into one hunk.
		j := i
		for j+1 < len(changed) && changed[j+1].first-(changed[j].first+len(changed[j].oldLines)) <= 2*previewContext {
			j++
		}
		hunkStart := max(0, changed[i].first-previewContext)
		hunkEnd := min(len(lines), changed[j].first+len(changed[j].oldLines)+previewContext)
		oldCount, newCount := hunkEnd-hunkStart, hunkEnd-hunkStart
		for k := i; k <= j; k++ {
			newCount += len(changed[k].newLines) - len(changed[k].oldLines)
		}
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", hunkStart+1, oldCount, hunkStart+1+delta, newCount)
		cursor := hunkStart
		for k := i; k <= j; k++ {
			for ; cursor < changed[k].first; cursor++ {
				writeLine(&b, ' ', lines[cursor])
			}
			for _, l := range changed[k].oldLines {
				writeLine(&b, '-', l)
			}
			for _, l := range changed[k].newLines {
				writeLine(&b, '+', l)
			}
			cursor = changed[k].first + len(changed[k].oldLines)
			total += len(changed[k].oldLines) + len(changed[k].newLines)
			delta += len(changed[k].newLines) - len(changed[k].oldLines)
		}
		for ; cursor < hunkEnd; cursor++ {
			writeLine(&b, ' ', lines[cursor])
		}
		i = j + 1
	}
	return b.String(), total
}

// createPreview renders the whole content of a new file, and any directories made for it.
func createPreview(path, content string, madeDirs []string) (string, int) {
	lines := splitKeep(content)
	var b strings.Builder
	fmt.Fprintf(&b, "--- /dev/null\n+++ b/%s\n", visible(path))
	if len(madeDirs) > 0 {
		fmt.Fprintf(&b, "(creates directories: %s)\n", visible(strings.Join(madeDirs, ", ")))
	}
	fmt.Fprintf(&b, "@@ -0,0 +1,%d @@\n", len(lines))
	for _, l := range lines {
		writeLine(&b, '+', l)
	}
	return b.String(), len(lines)
}

// deletePreview describes a deleted file and shows its first lines.
func deletePreview(path, content string) (string, int) {
	lines := splitKeep(content)
	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ /dev/null\n(deletes the whole file: %d %s, %d bytes)\n", visible(path), len(lines), plural(len(lines), "line", "lines"), len(content))
	shown := min(len(lines), deletePreviewLines)
	for _, l := range lines[:shown] {
		writeLine(&b, '-', l)
	}
	if shown < len(lines) {
		fmt.Fprintf(&b, "[%d more lines not shown]\n", len(lines)-shown)
	}
	return b.String(), len(lines)
}

// previewFits reports whether a preview can be shown whole. A change a person cannot read
// in full is not offered for approval: the model must split it into smaller ones.
func previewFits(text string) bool { return len(text) <= harness.MaxPreviewBytes }
