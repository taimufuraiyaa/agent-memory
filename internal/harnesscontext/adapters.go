package harnesscontext

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/harnessfs"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessrun"
)

const (
	// MaxRepositoryFileBytes bounds how much of one file is ever read.
	MaxRepositoryFileBytes = 256 << 10
	maxInstructionBytes    = 64 << 10
)

// instructionFiles are the only repository files treated as project instructions, and
// only at the project root. They are pinned but still quoted and still carry no
// authority: a cloned repository's instructions cannot grant tools or approvals.
var instructionFiles = []string{"CLAUDE.md", "AGENTS.md"}

// FromRun turns a run's own bounded chunks into evidence. The goal and the client's
// answers are the user's words; model text and tool output are untrusted, and later
// chunks rank higher so recent context wins under pressure.
func FromRun(workspace string, chunks []harnessrun.Chunk) []Chunk {
	out := make([]Chunk, 0, len(chunks))
	last := maxInt(1, len(chunks)-1)
	for i, c := range chunks {
		chunk := Chunk{ID: "run-" + sanitizeID(c.ID), Workspace: workspace, Source: SourceRun, Ref: "run:" + c.Kind,
			Revision: fmt.Sprintf("turn%d", c.Turn), Title: c.Kind, Text: c.Text, Sensitivity: SensitivityInternal,
			Trust: TrustUntrusted, Relevance: 0.5 + 0.4*float64(i)/float64(last)}
		switch c.Kind {
		case "goal":
			chunk.Trust, chunk.Pinned, chunk.Relevance = TrustUser, true, 1
		case "input":
			chunk.Trust, chunk.Relevance = TrustUser, 0.95
		}
		out = append(out, chunk)
	}
	return out
}

func sanitizeID(id string) string {
	var b strings.Builder
	for _, r := range id {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' || r == ':' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "x"
	}
	if b.Len() > 56 {
		return b.String()[:56]
	}
	return b.String()
}

// MemoryHit is one recalled memory or solution record, in the shape the recall
// services return it.
type MemoryHit struct {
	ID          string
	Workspace   string
	Content     string
	Revision    string
	Sensitivity string
	Score       float64
	Superseded  bool
	Suppressed  bool
}

// FromMemory converts recalled records into evidence. Recalled content may include text
// captured from earlier sessions, so it is untrusted; an unknown sensitivity label is
// treated as restricted so a mislabelled record is never over-shared; superseded and
// suppressed records are marked so they are excluded, not shown.
func FromMemory(source Source, hits []MemoryHit) []Chunk {
	out := make([]Chunk, 0, len(hits))
	for _, h := range hits {
		sens, _ := ParseSensitivity(h.Sensitivity)
		chunk := Chunk{ID: string(source) + "-" + sanitizeID(h.ID), Workspace: h.Workspace, Source: source, Ref: h.ID, Revision: h.Revision,
			Text: h.Content, Sensitivity: sens, Trust: TrustUntrusted, Relevance: h.Score}
		if h.Superseded || h.Suppressed {
			chunk.Lifecycle = "superseded"
		}
		out = append(out, chunk)
	}
	return out
}

// reasonFor maps a confined-read failure to a content-free exclusion reason.
func reasonFor(err error) string {
	switch {
	case errors.Is(err, harnessfs.ErrInvalidPath), errors.Is(err, harnessfs.ErrDenied):
		return ReasonDenied
	case errors.Is(err, harnessfs.ErrBinary):
		return ReasonBinary
	}
	return ReasonUnreadable
}

// InstructionChunks reads the known instruction files at the project root. Reads are
// confined to the root by the operating system, so a symlinked instruction file cannot
// reach outside the project.
func InstructionChunks(workspace, root string) ([]Chunk, []Exclusion) {
	project, err := harnessfs.Open(root)
	if err != nil {
		return nil, nil
	}
	defer project.Close()
	var chunks []Chunk
	var excluded []Exclusion
	for _, name := range instructionFiles {
		// Exists does not follow the entry, so a symlink the root refuses to follow is still
		// seen as present and reported, while a genuinely absent file is skipped quietly.
		if !project.Exists(name) {
			continue
		}
		id := "instr-" + sanitizeID(name)
		file, err := project.Read(name, maxInstructionBytes)
		if err != nil {
			excluded = append(excluded, Exclusion{ID: id, Reason: reasonFor(err)})
			continue
		}
		chunks = append(chunks, Chunk{ID: id, Workspace: workspace, Source: SourceInstruction, Ref: name, Revision: file.Revision, Title: name,
			Text: string(file.Data), Sensitivity: SensitivityInternal, Trust: TrustProject, Pinned: true, Relevance: 1})
	}
	return chunks, excluded
}

// PathHint asks for one repository file with a relevance the caller determined.
type PathHint struct {
	Path      string
	Relevance float64
}

// RepositoryChunks reads requested files as untrusted evidence. A path must be local to
// the root, must not be a hidden or credential-like name, must be a regular text file,
// and is read through a root-confined handle so symlinks and `..` cannot escape.
func RepositoryChunks(workspace, root string, hints []PathHint) ([]Chunk, []Exclusion) {
	project, err := harnessfs.Open(root)
	if err != nil {
		return nil, []Exclusion{{ID: "repo", Reason: ReasonUnreadable}}
	}
	defer project.Close()
	var chunks []Chunk
	var excluded []Exclusion
	for _, hint := range hints {
		id := repoID(hint.Path)
		cleaned, err := harnessfs.Clean(hint.Path)
		if err != nil || cleaned == "." {
			excluded = append(excluded, Exclusion{ID: id, Reason: ReasonDenied})
			continue
		}
		file, err := project.Read(cleaned, MaxRepositoryFileBytes)
		if err != nil {
			excluded = append(excluded, Exclusion{ID: id, Reason: reasonFor(err)})
			continue
		}
		chunks = append(chunks, Chunk{ID: id, Workspace: workspace, Source: SourceRepository, Ref: filepath.ToSlash(cleaned), Revision: file.Revision,
			Title: filepath.Base(cleaned), Text: string(file.Data), Sensitivity: SensitivityInternal, Trust: TrustUntrusted, Relevance: hint.Relevance})
	}
	return chunks, excluded
}

// RepositoryRevalidator reports a repository chunk's current revision, so a file that
// changed after it was gathered is dropped as stale instead of being shown out of date.
// Chunks from other sources are not repository files and pass through unchanged.
func RepositoryRevalidator(root string) func(context.Context, Chunk) (string, bool) {
	return func(_ context.Context, c Chunk) (string, bool) {
		if c.Source != SourceRepository && c.Source != SourceInstruction {
			return c.Revision, true
		}
		project, err := harnessfs.Open(root)
		if err != nil {
			return "", false
		}
		defer project.Close()
		limit := MaxRepositoryFileBytes
		if c.Source == SourceInstruction {
			limit = maxInstructionBytes
		}
		file, err := project.Read(filepath.FromSlash(c.Ref), limit)
		if err != nil {
			return "", false
		}
		return file.Revision, true
	}
}

func repoID(path string) string {
	sum := sha256.Sum256([]byte(path))
	return "repo-" + hex.EncodeToString(sum[:])[:24]
}
