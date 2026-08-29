package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

const legacySkillsRoot = ".agents/skills"

type SkillMaterializationRequest = core.SkillMaterializationRequest

type SkillMaterializationResult = core.SkillMaterializationResult

type SkillMaterializer struct {
	projectRoot     string
	skillsRoot      string
	bundles         *RevisionBundleStore
	afterPriorMoved func() error
}

func NewSkillMaterializer(projectRoot string, bundles *RevisionBundleStore) (*SkillMaterializer, error) {
	if bundles == nil {
		return nil, errors.New("revision bundle store is required")
	}
	projectRoot = filepath.Clean(strings.TrimSpace(projectRoot))
	validated, err := os.Lstat(projectRoot)
	if err != nil || !validated.IsDir() || validated.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("registered project root is invalid")
	}
	root, err := os.OpenRoot(projectRoot)
	if err != nil {
		return nil, errors.New("open registered project root")
	}
	defer root.Close()
	if err := ensureRootDirectories(root, legacySkillsRoot); err != nil {
		return nil, err
	}
	return &SkillMaterializer{
		projectRoot: projectRoot,
		skillsRoot:  filepath.Join(projectRoot, filepath.FromSlash(legacySkillsRoot)),
		bundles:     bundles,
	}, nil
}

func (m *SkillMaterializer) Materialize(ctx context.Context, request SkillMaterializationRequest) (SkillMaterializationResult, error) {
	result := SkillMaterializationResult{
		OperationID: request.OperationID,
		SkillID:     request.Skill.ID,
		RevisionID:  request.Revision.ID,
		Digest:      request.Revision.BundleDigest,
	}
	if m == nil {
		return result, errors.New("skill materializer is required")
	}
	if err := validateMaterializationRequest(request); err != nil {
		return result, err
	}
	contents, err := m.bundles.Read(ctx, request.Revision)
	if err != nil {
		return result, fmt.Errorf("verify immutable source bundle: %w", err)
	}
	root, rootInfo, directoryHandle, err := openContainedDirectory(m.skillsRoot, "legacy skills root")
	if err != nil {
		return result, err
	}
	defer root.Close()
	defer directoryHandle.Close()
	if rootInfo.Mode().Perm()&0o200 == 0 {
		return result, errors.New("legacy skills root is not writable")
	}
	activeName := request.Skill.Name
	stageName := "." + activeName + ".stage-" + request.OperationID
	backupName := "." + activeName + ".backup-" + request.OperationID

	recovered, complete, err := recoverMaterialization(ctx, root, activeName, stageName, backupName, request.Revision)
	if err != nil {
		return result, err
	}
	result.Recovered = recovered
	if complete {
		return result, nil
	}
	if err := requireUnchangedDirectory(m.skillsRoot, rootInfo, "legacy skills root"); err != nil {
		return result, err
	}
	activeExists, err := validateOptionalActiveBundle(root, activeName)
	if err != nil {
		return result, err
	}
	if err := root.Mkdir(stageName, 0o700); err != nil {
		return result, errors.New("create staged active skill bundle")
	}
	stageCommitted := false
	defer func() {
		if !stageCommitted {
			removeSkillBundleStage(root, stageName)
		}
	}()
	stage, err := root.OpenRoot(stageName)
	if err != nil {
		return result, errors.New("open staged active skill bundle")
	}
	if err := writeActiveSkillBundle(ctx, stage, request.Revision, contents); err != nil {
		stage.Close()
		return result, err
	}
	if err := makeStagedBundleReadOnly(stage); err != nil {
		stage.Close()
		return result, err
	}
	if err := stage.Close(); err != nil {
		return result, errors.New("close staged active skill bundle")
	}
	if err := requireUnchangedDirectory(m.skillsRoot, rootInfo, "legacy skills root"); err != nil {
		return result, err
	}
	priorMoved := false
	if activeExists {
		if err := root.Rename(activeName, backupName); err != nil {
			return result, errors.New("reserve prior active skill bundle")
		}
		priorMoved = true
	}
	restorePrior := func() error {
		if priorMoved {
			if err := root.Rename(backupName, activeName); err != nil {
				return errors.New("restore prior active skill bundle")
			}
		}
		return nil
	}
	if m.afterPriorMoved != nil {
		if err := m.afterPriorMoved(); err != nil {
			if restoreErr := restorePrior(); restoreErr != nil {
				return result, errors.Join(err, restoreErr)
			}
			return result, err
		}
	}
	if err := root.Rename(stageName, activeName); err != nil {
		if restoreErr := restorePrior(); restoreErr != nil {
			return result, errors.Join(errors.New("activate staged skill bundle"), restoreErr)
		}
		return result, errors.New("activate staged skill bundle")
	}
	stageCommitted = true
	if _, err := readActiveSkillBundle(ctx, root, activeName, request.Revision); err != nil {
		removeSkillBundleStage(root, activeName)
		if restoreErr := restorePrior(); restoreErr != nil {
			return result, errors.Join(fmt.Errorf("verify materialized skill bundle: %w", err), restoreErr)
		}
		return result, fmt.Errorf("verify materialized skill bundle: %w", err)
	}
	if err := directoryHandle.Sync(); err != nil {
		return result, errors.New("sync legacy skills root")
	}
	if priorMoved {
		removeSkillBundleStage(root, backupName)
	}
	if err := requireUnchangedDirectory(m.skillsRoot, rootInfo, "legacy skills root"); err != nil {
		return result, err
	}
	return result, nil
}

func validateMaterializationRequest(request SkillMaterializationRequest) error {
	if !safeMaterializationComponent(request.OperationID) {
		return errors.New("materialization operation_id is unsafe")
	}
	if err := request.Skill.Validate(); err != nil {
		return err
	}
	if err := request.Revision.Validate(); err != nil {
		return err
	}
	if !safeMaterializationComponent(request.Skill.Name) {
		return errors.New("logical skill name is unsafe for materialization")
	}
	if request.Skill.ID != request.Revision.SkillID || request.Skill.Workspace != request.Revision.Workspace {
		return errors.New("skill and revision scope do not match")
	}
	return nil
}

func safeMaterializationComponent(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || value == "." || value == ".." || filepath.Base(value) != value || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_' || character == '.' {
			continue
		}
		return false
	}
	return true
}

func openContainedDirectory(path, label string) (*os.Root, os.FileInfo, *os.File, error) {
	validated, err := os.Lstat(path)
	if err != nil || !validated.IsDir() || validated.Mode()&os.ModeSymlink != 0 {
		return nil, nil, nil, fmt.Errorf("%s is invalid", label)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open %s", label)
	}
	handle, err := root.Open(".")
	if err != nil {
		root.Close()
		return nil, nil, nil, fmt.Errorf("open %s descriptor", label)
	}
	opened, err := handle.Stat()
	if err != nil || !opened.IsDir() || !os.SameFile(validated, opened) {
		handle.Close()
		root.Close()
		return nil, nil, nil, fmt.Errorf("%s changed while opening", label)
	}
	return root, opened, handle, nil
}

func recoverMaterialization(ctx context.Context, root *os.Root, activeName, stageName, backupName string, revision core.SkillRevision) (bool, bool, error) {
	backupExists, err := rootEntryExists(root, backupName)
	if err != nil {
		return false, false, err
	}
	activeExists, err := rootEntryExists(root, activeName)
	if err != nil {
		return false, false, err
	}
	if backupExists && activeExists {
		if _, err := readActiveSkillBundle(ctx, root, activeName, revision); err != nil {
			return false, false, errors.New("ambiguous interrupted materialization requires operator recovery")
		}
		removeSkillBundleStage(root, backupName)
		removeSkillBundleStage(root, stageName)
		return true, true, nil
	}
	if backupExists && !activeExists {
		if err := root.Rename(backupName, activeName); err != nil {
			return false, false, errors.New("restore interrupted prior skill bundle")
		}
		removeSkillBundleStage(root, stageName)
		return true, false, nil
	}
	if !backupExists {
		removeSkillBundleStage(root, stageName)
	}
	return false, false, nil
}

func validateOptionalActiveBundle(root *os.Root, name string) (bool, error) {
	info, err := root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, errors.New("prior active skill bundle is invalid")
	}
	err = fs.WalkDir(root.FS(), name, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("prior active skill bundle contains a symlink")
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("prior active skill bundle contains a non-regular file")
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return true, nil
}

func writeActiveSkillBundle(ctx context.Context, root *os.Root, revision core.SkillRevision, contents map[string][]byte) error {
	for _, declared := range revision.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := ensureRootDirectories(root, filepath.ToSlash(filepath.Dir(declared.Path))); err != nil {
			return err
		}
		file, err := root.OpenFile(declared.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o400)
		if err != nil {
			return fmt.Errorf("create active skill file %q: %w", declared.Path, err)
		}
		if _, err := file.Write(contents[declared.Path]); err != nil {
			file.Close()
			return fmt.Errorf("write active skill file %q: %w", declared.Path, err)
		}
		if err := file.Sync(); err != nil {
			file.Close()
			return fmt.Errorf("sync active skill file %q: %w", declared.Path, err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("close active skill file %q: %w", declared.Path, err)
		}
	}
	return syncRootDirectories(root)
}

func readActiveSkillBundle(ctx context.Context, root *os.Root, name string, revision core.SkillRevision) (map[string][]byte, error) {
	active, err := root.OpenRoot(name)
	if err != nil {
		return nil, errors.New("open active skill bundle")
	}
	defer active.Close()
	seen := make(map[string]struct{}, len(revision.Files))
	err = fs.WalkDir(active.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("active skill bundle contains a symlink")
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("active skill bundle contains a non-regular file")
		}
		seen[strings.TrimPrefix(filepath.ToSlash(path), "./")] = struct{}{}
		return nil
	})
	if err != nil || len(seen) != len(revision.Files) {
		return nil, errors.New("active skill bundle inventory does not match revision")
	}
	contents := make(map[string][]byte, len(revision.Files))
	for _, declared := range revision.Files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, exists := seen[declared.Path]; !exists {
			return nil, fmt.Errorf("active skill file %q is missing", declared.Path)
		}
		if err := requireRootRegularFile(active, declared.Path); err != nil {
			return nil, err
		}
		content, err := active.ReadFile(declared.Path)
		if err != nil || int64(len(content)) != declared.SizeBytes {
			return nil, fmt.Errorf("read active skill file %q", declared.Path)
		}
		digest := sha256.Sum256(content)
		if "sha256:"+hex.EncodeToString(digest[:]) != declared.Digest {
			return nil, fmt.Errorf("active skill file %q digest does not match", declared.Path)
		}
		contents[declared.Path] = content
	}
	if skillBundleDigest(revision.Files) != revision.BundleDigest {
		return nil, errors.New("active skill bundle digest does not match revision")
	}
	return contents, nil
}

func rootEntryExists(root *os.Root, name string) (bool, error) {
	_, err := root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
