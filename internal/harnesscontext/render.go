package harnesscontext

import (
	"fmt"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
)

const quotePrefix = "│ "

// renderBlock is the one place a chunk becomes prompt text, and token sizing measures
// exactly this string. The header carries provenance and the per-assembly random
// boundary, which untrusted text cannot know; every body line is prefixed with a quote
// marker, so no line of evidence can ever look like a header or a section edge.
func renderBlock(it Item, boundary string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "--- %s id=%s source=%s ref=%q rev=%q sens=%s trust=%s vis=%s", boundary, it.ID, it.Source, it.Ref, it.Revision, it.Sensitivity, it.Trust, it.Visibility)
	if it.Title != "" {
		fmt.Fprintf(&b, " title=%q", it.Title)
	}
	if it.Truncated {
		b.WriteString(" truncated")
	}
	if it.Suspicious {
		b.WriteString(" flagged-instruction-like")
	}
	b.WriteString(" ---\n")
	for _, line := range strings.Split(it.Text, "\n") {
		b.WriteString(quotePrefix)
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// Render produces the full prompt context: the fixed policy, the pinned instructions,
// then everything else as quoted evidence. Pinned items appear only under
// INSTRUCTIONS; nothing else can, whatever it claims about itself.
func Render(a Assembled) string {
	var b strings.Builder
	b.WriteString(PolicyText)
	b.WriteString("\n")
	b.WriteString(sectionLine("BEGIN", "INSTRUCTIONS", a.Boundary))
	for _, it := range a.Items {
		if it.Pinned {
			b.WriteString(renderBlock(it, a.Boundary))
		}
	}
	b.WriteString(sectionLine("END", "INSTRUCTIONS", a.Boundary))
	b.WriteString(sectionLine("BEGIN", "EVIDENCE", a.Boundary))
	for _, it := range a.Items {
		if !it.Pinned {
			b.WriteString(renderBlock(it, a.Boundary))
		}
	}
	b.WriteString(sectionLine("END", "EVIDENCE", a.Boundary))
	return b.String()
}

// EvidenceRefs lists the included items as harness references, keeping source,
// revision and class so provenance survives into the provider request.
func (a Assembled) EvidenceRefs() []harness.EvidenceRef {
	refs := make([]harness.EvidenceRef, 0, len(a.Items))
	for _, it := range a.Items {
		if len(refs) == harness.MaxEvidenceRefs {
			break
		}
		refs = append(refs, harness.EvidenceRef{ID: it.ID, Revision: cleanLine(it.Revision, 64), Class: string(it.Source)})
	}
	return refs
}
