package harnesstools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessfs"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessproof"
)

const (
	ToolEditFile   = "edit_file"
	ToolCreateFile = "create_file"
	ToolDeleteFile = "delete_file"

	MaxEdits            = 20
	MaxEditTextBytes    = 32 << 10
	MaxCreateBytes      = 32 << 10
	MaxMutationArgBytes = 90 << 10
	maxPendingMutations = 8
)

// Mutating reports whether a capability changes files. Policy never lets one of these run
// without a person's approval, whatever its table says.
func Mutating(capability harness.CapabilityID) bool {
	switch capability {
	case ToolEditFile, ToolCreateFile, ToolDeleteFile, ToolRunCommand, ToolGitStage, ToolGitCommit:
		return true
	}
	return false
}

// EditConfig turns the mutating tools on. The zero value leaves them off: the provider
// then offers only the read tools, which is the rollback for this whole stage.
type EditConfig struct {
	Enabled bool
	// PreimageDir receives a private copy of each file before it is changed, so an applied
	// action can be undone. It is required when Enabled and must be outside the project.
	PreimageDir string
}

type editSpec struct {
	OldText string `json:"old_text"`
	NewText string `json:"new_text"`
}

type editArgs struct {
	Path  string     `json:"path"`
	Edits []editSpec `json:"edits"`
}

type createArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type deleteArgs struct {
	Path string `json:"path"`
}

// plan is a fully computed, not yet applied change.
type plan struct {
	tool         string
	path         string
	preRevision  string
	postRevision string
	mode         fs.FileMode
	original     []byte
	updated      []byte
	madeDirs     []string
}

func textOK(value string, max int) bool {
	return len(value) <= max && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

// planFailure maps a confined-filesystem error to a typed outcome and a fixed reason.
func planFailure(err error) (harness.Outcome, string) {
	switch {
	case errors.Is(err, harnessfs.ErrDenied), errors.Is(err, harnessfs.ErrInvalidPath):
		return harness.OutcomeDenied, ""
	case errors.Is(err, harnessfs.ErrBinary):
		return harness.OutcomeFailed, "not_text"
	case errors.Is(err, harnessfs.ErrTooLarge):
		return harness.OutcomeFailed, "too_large"
	case errors.Is(err, harnessfs.ErrExists):
		return harness.OutcomeFailed, "exists"
	}
	return harness.OutcomeFailed, "unreadable"
}

func planEdit(project *harnessfs.Root, raw []byte) (*plan, harness.Outcome, string) {
	var a editArgs
	if decodeStrict(raw, &a) != nil || len(a.Edits) == 0 || len(a.Edits) > MaxEdits {
		return nil, harness.OutcomeFailed, "invalid_arguments"
	}
	clean, outcome := cleanPath(a.Path, false)
	if outcome != "" {
		return nil, outcome, ""
	}
	for _, e := range a.Edits {
		if e.OldText == "" || !textOK(e.OldText, MaxEditTextBytes) || !textOK(e.NewText, MaxEditTextBytes) {
			return nil, harness.OutcomeFailed, "invalid_arguments"
		}
	}
	data, revision, mode, err := project.ReadWhole(clean)
	if err != nil {
		outcome, reason := planFailure(err)
		return nil, outcome, reason
	}
	if !utf8.Valid(data) {
		return nil, harness.OutcomeFailed, "not_text"
	}
	original := string(data)
	spans := make([]span, 0, len(a.Edits))
	for _, e := range a.Edits {
		at := strings.Index(original, e.OldText)
		if at < 0 {
			return nil, harness.OutcomeFailed, "no_match"
		}
		if strings.Contains(original[at+1:], e.OldText) {
			return nil, harness.OutcomeFailed, "ambiguous_match"
		}
		spans = append(spans, span{start: at, end: at + len(e.OldText), text: e.NewText})
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	for i := 1; i < len(spans); i++ {
		if spans[i-1].end > spans[i].start {
			return nil, harness.OutcomeFailed, "overlapping_edits"
		}
	}
	var updated strings.Builder
	cursor := 0
	for _, sp := range spans {
		updated.WriteString(original[cursor:sp.start])
		updated.WriteString(sp.text)
		cursor = sp.end
	}
	updated.WriteString(original[cursor:])
	if updated.String() == original {
		return nil, harness.OutcomeFailed, "no_change"
	}
	if updated.Len() > harnessfs.MaxWriteBytes {
		return nil, harness.OutcomeFailed, "too_large"
	}
	return &plan{tool: ToolEditFile, path: clean, preRevision: revision, postRevision: harnessfs.Revision([]byte(updated.String())), mode: mode,
		original: data, updated: []byte(updated.String())}, harness.OutcomeOK, ""
}

func planCreate(project *harnessfs.Root, raw []byte) (*plan, harness.Outcome, string) {
	var a createArgs
	if decodeStrict(raw, &a) != nil || !textOK(a.Content, MaxCreateBytes) {
		return nil, harness.OutcomeFailed, "invalid_arguments"
	}
	clean, outcome := cleanPath(a.Path, false)
	if outcome != "" {
		return nil, outcome, ""
	}
	// Parents first, so a path under a file is reported as that rather than as unreadable.
	parts := strings.Split(clean, "/")
	var made []string
	for i := 1; i < len(parts); i++ {
		prefix := strings.Join(parts[:i], "/")
		parent, err := project.Inspect(prefix)
		if err != nil {
			outcome, reason := planFailure(err)
			return nil, outcome, reason
		}
		if parent.Exists && !parent.Dir {
			return nil, harness.OutcomeFailed, "parent_is_a_file"
		}
		if !parent.Exists {
			made = append(made, prefix)
		}
	}
	info, err := project.Inspect(clean)
	if err != nil {
		outcome, reason := planFailure(err)
		return nil, outcome, reason
	}
	if info.Exists {
		return nil, harness.OutcomeFailed, "exists"
	}
	return &plan{tool: ToolCreateFile, path: clean, preRevision: harnessfs.AbsentRevision, postRevision: harnessfs.Revision([]byte(a.Content)), mode: 0o644,
		updated: []byte(a.Content), madeDirs: made}, harness.OutcomeOK, ""
}

func planDelete(project *harnessfs.Root, raw []byte) (*plan, harness.Outcome, string) {
	var a deleteArgs
	if decodeStrict(raw, &a) != nil {
		return nil, harness.OutcomeFailed, "invalid_arguments"
	}
	clean, outcome := cleanPath(a.Path, false)
	if outcome != "" {
		return nil, outcome, ""
	}
	data, revision, mode, err := project.ReadWhole(clean)
	if err != nil {
		outcome, reason := planFailure(err)
		return nil, outcome, reason
	}
	if !utf8.Valid(data) {
		return nil, harness.OutcomeFailed, "not_text"
	}
	return &plan{tool: ToolDeleteFile, path: clean, preRevision: revision, postRevision: harnessfs.AbsentRevision, mode: mode, original: data}, harness.OutcomeOK, ""
}

// prepareMutation validates one mutating call and computes the whole change without
// writing anything. The digest covers the tool, the canonical arguments, the project root
// and the revision of the file as it is now, so an approval of this digest can only ever
// run this exact change against this exact content.
func (s *session) prepareMutation(q harness.ToolRequest, root string, action harness.PreparedAction) harness.PreparedAction {
	fail := func(outcome harness.Outcome, reason string) harness.PreparedAction {
		action.Outcome, action.Reason = outcome, reason
		return action
	}
	if !s.provider.cfg.Edit.Enabled {
		return fail(harness.OutcomeUnsupported, "")
	}
	if len(q.Arguments) > MaxMutationArgBytes {
		return fail(harness.OutcomeFailed, "arguments_too_large")
	}
	project, err := harnessfs.Open(root)
	if err != nil {
		return fail(harness.OutcomeUnavailable, "")
	}
	defer project.Close()
	var p *plan
	var outcome harness.Outcome
	var reason string
	switch q.ToolID {
	case ToolEditFile:
		p, outcome, reason = planEdit(project, q.Arguments)
	case ToolCreateFile:
		p, outcome, reason = planCreate(project, q.Arguments)
	case ToolDeleteFile:
		p, outcome, reason = planDelete(project, q.Arguments)
	default:
		return fail(harness.OutcomeUnsupported, "")
	}
	if outcome != harness.OutcomeOK {
		return fail(outcome, reason)
	}
	var preview, summary string
	var changed int
	var escalate []string
	switch p.tool {
	case ToolEditFile:
		var a editArgs
		_ = decodeStrict(q.Arguments, &a)
		spans := editSpans(string(p.original), a.Edits)
		preview, changed = editPreview(p.path, string(p.original), spans)
		summary = fmt.Sprintf("edit %s (%d %s)", p.path, len(a.Edits), plural(len(a.Edits), "edit", "edits"))
	case ToolCreateFile:
		preview, changed = createPreview(p.path, string(p.updated), p.madeDirs)
		summary = "create " + p.path
	case ToolDeleteFile:
		preview, changed = deletePreview(p.path, string(p.original))
		summary = "delete " + p.path
		escalate = append(escalate, "delete")
	}
	if !previewFits(preview) {
		return fail(harness.OutcomeFailed, "change_too_large")
	}
	if changed >= largeChangeLines {
		escalate = append(escalate, "large_change")
	}
	if p.mode&0o111 != 0 {
		escalate = append(escalate, "executable")
	}
	if strings.HasPrefix(string(p.updated), "#!") || strings.HasPrefix(string(p.original), "#!") {
		escalate = append(escalate, "shebang")
	}
	if len(p.madeDirs) > 0 {
		escalate = append(escalate, "new_directories")
	}
	canonical, err := json.Marshal(struct {
		Tool string `json:"tool"`
		Path string `json:"path"`
		Pre  string `json:"pre"`
		Post string `json:"post"`
		Mode uint32 `json:"mode"`
		Made string `json:"made"`
	}{p.tool, p.path, p.preRevision, p.postRevision, uint32(p.mode), strings.Join(p.madeDirs, ",")})
	if err != nil {
		return fail(harness.OutcomeFailed, "")
	}
	sum := sha256.Sum256([]byte(p.tool + "\x00" + string(canonical) + "\x00" + root))
	digest := "sha256:" + hex.EncodeToString(sum[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) >= maxPending || s.pendingMutations() >= maxPendingMutations {
		return fail(harness.OutcomeFailed, "busy")
	}
	s.pending[digest] = call{tool: q.ToolID, root: root, plan: p}
	action.Outcome, action.Digest, action.Summary = harness.OutcomeOK, digest, summary
	action.Paths, action.Preview, action.Escalate = []string{p.path}, preview, escalate
	return action
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// editSpans locates each replacement in the original. planEdit has already proved each
// matches exactly once and that none overlap.
func editSpans(original string, edits []editSpec) []span {
	spans := make([]span, 0, len(edits))
	for _, e := range edits {
		at := strings.Index(original, e.OldText)
		spans = append(spans, span{start: at, end: at + len(e.OldText), text: e.NewText})
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	return spans
}

// applyHook lets a test change the file between the write and its verification, to prove a
// result that does not verify is rolled back. It is nil outside tests.
var applyHook func(project *harnessfs.Root, p *plan)

// apply performs one prepared, approved change. It refuses unless the context carries the
// manager's proof for exactly this digest. The original is saved first, the file is
// replaced only if it still has the reviewed revision, and the result is read back and
// verified; a result that does not verify is rolled back.
func (s *session) apply(ctx context.Context, project *harnessfs.Root, prepared call, action harness.PreparedAction) ([]byte, harness.Outcome) {
	if action.Digest == "" || harnessproof.Approved(ctx) != action.Digest {
		return nil, harness.OutcomeDenied
	}
	p := prepared.plan
	dir := s.provider.cfg.Edit.PreimageDir
	record := preimage{Version: preimageVersion, Digest: action.Digest, Workspace: s.scope.Workspace, Root: prepared.root, Tool: p.tool, Path: p.path,
		Existed: p.tool != ToolCreateFile, PreRevision: p.preRevision, PostRevision: p.postRevision, Mode: uint32(p.mode), Data: p.original, AppliedAt: time.Now().UTC()}
	if err := stagePreimage(dir, record); err != nil {
		return nil, harness.OutcomeFailed
	}
	var err error
	var made []string
	switch p.tool {
	case ToolEditFile:
		err = project.ReplaceFile(p.path, p.updated, p.preRevision)
	case ToolCreateFile:
		made, err = project.CreateFile(p.path, p.updated, p.mode)
	case ToolDeleteFile:
		err = project.RemoveFile(p.path, p.preRevision)
	}
	if err != nil {
		discardPreimage(dir, action.Digest)
		switch {
		case errors.Is(err, harnessfs.ErrStale), errors.Is(err, harnessfs.ErrExists):
			return nil, harness.OutcomeStale
		case errors.Is(err, harnessfs.ErrDenied), errors.Is(err, harnessfs.ErrInvalidPath):
			return nil, harness.OutcomeDenied
		}
		return nil, harness.OutcomeFailed
	}
	record.CreatedDirs = made
	if err := stagePreimage(dir, record); err != nil || commitPreimage(dir, action.Digest) != nil {
		// The change is in place but its saved copy could not be kept: put the file back
		// rather than leave a change that cannot be undone.
		discardPreimage(dir, action.Digest)
		s.rollback(project, p)
		return nil, harness.OutcomeFailed
	}
	prunePreimages(dir, time.Now())
	if applyHook != nil {
		applyHook(project, p)
	}
	if !s.verified(project, p) {
		s.rollback(project, p)
		return nil, harness.OutcomeFailed
	}
	verb := map[string]string{ToolEditFile: "edited", ToolCreateFile: "created", ToolDeleteFile: "deleted"}[p.tool]
	return []byte(s.provider.cfg.Redact(fmt.Sprintf("%s %s; revision %s (was %s)", verb, p.path, p.postRevision, p.preRevision))), harness.OutcomeOK
}

// verified reads the result back and compares it with what was meant to be written.
func (s *session) verified(project *harnessfs.Root, p *plan) bool {
	if p.tool == ToolDeleteFile {
		info, err := project.Inspect(p.path)
		return err == nil && !info.Exists
	}
	_, revision, _, err := project.ReadWhole(p.path)
	return err == nil && revision == p.postRevision
}

// rollback puts the previous state back after a change that did not verify.
func (s *session) rollback(project *harnessfs.Root, p *plan) {
	switch p.tool {
	case ToolEditFile:
		if _, revision, _, err := project.ReadWhole(p.path); err == nil {
			_ = project.ReplaceFile(p.path, p.original, revision)
		}
	case ToolCreateFile:
		if _, revision, _, err := project.ReadWhole(p.path); err == nil {
			_ = project.RemoveFile(p.path, revision)
		}
	case ToolDeleteFile:
		_, _ = project.CreateFile(p.path, p.original, p.mode)
	}
}
