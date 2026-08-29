package workspace

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

const (
	skillRevisionObjectRoot = ".agent-memory/skill-revisions/objects/sha256"
	skillBundleManifestName = "manifest.json"
	skillBundleDirectory    = "bundle"
)

type PublishedSkillBundle struct {
	Digest       string `json:"digest"`
	RelativePath string `json:"relative_path"`
	Duplicate    bool   `json:"duplicate"`
}

type RevisionBundleStore struct {
	projectRoot   string
	objectRoot    string
	afterRootOpen func()
	beforeCommit  func() error
}

type storedSkillBundleManifest struct {
	ManifestVersion int64                  `json:"manifest_version"`
	BundleDigest    string                 `json:"bundle_digest"`
	Files           []core.SkillBundleFile `json:"files"`
}

func NewRevisionBundleStore(projectRoot string) (*RevisionBundleStore, error) {
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
	opened, err := root.Open(".")
	if err != nil {
		return nil, errors.New("open registered project root descriptor")
	}
	defer opened.Close()
	openedInfo, err := opened.Stat()
	if err != nil || !os.SameFile(validated, openedInfo) {
		return nil, errors.New("registered project root changed while opening")
	}
	if err := ensureRootDirectories(root, skillRevisionObjectRoot); err != nil {
		return nil, err
	}
	if err := requireUnchangedDirectory(projectRoot, openedInfo, "registered project root"); err != nil {
		return nil, err
	}
	return &RevisionBundleStore{
		projectRoot: projectRoot,
		objectRoot:  filepath.Join(projectRoot, filepath.FromSlash(skillRevisionObjectRoot)),
	}, nil
}

func (s *RevisionBundleStore) Publish(ctx context.Context, revision core.SkillRevision, contents map[string][]byte) (PublishedSkillBundle, error) {
	result := PublishedSkillBundle{}
	if s == nil {
		return result, errors.New("revision bundle store is required")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := validateSkillBundleContents(revision, contents); err != nil {
		return result, err
	}
	objectName := strings.TrimPrefix(revision.BundleDigest, "sha256:")
	result.Digest = revision.BundleDigest
	result.RelativePath = filepath.ToSlash(filepath.Join(skillRevisionObjectRoot, objectName))

	root, rootInfo, directoryHandle, err := s.openObjectRoot()
	if err != nil {
		return result, err
	}
	defer root.Close()
	defer directoryHandle.Close()
	if s.afterRootOpen != nil {
		s.afterRootOpen()
	}
	if err := requireUnchangedDirectory(s.objectRoot, rootInfo, "skill revision object root"); err != nil {
		return result, err
	}
	if _, err := root.Lstat(objectName); err == nil {
		if _, verifyErr := readSkillBundleFromRoot(ctx, root, objectName, revision); verifyErr != nil {
			return result, fmt.Errorf("existing immutable bundle is invalid: %w", verifyErr)
		}
		result.Duplicate = true
		return result, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return result, errors.New("inspect immutable bundle destination")
	}

	stageName, err := createSkillBundleStage(root)
	if err != nil {
		return result, err
	}
	committed := false
	defer func() {
		if !committed {
			removeSkillBundleStage(root, stageName)
		}
	}()
	stage, err := root.OpenRoot(stageName)
	if err != nil {
		return result, errors.New("open staged skill bundle")
	}
	if err := writeStagedSkillBundle(ctx, stage, revision, contents); err != nil {
		stage.Close()
		return result, err
	}
	if s.beforeCommit != nil {
		if err := s.beforeCommit(); err != nil {
			stage.Close()
			return result, err
		}
	}
	if err := makeStagedBundleReadOnly(stage); err != nil {
		stage.Close()
		return result, err
	}
	if err := stage.Close(); err != nil {
		return result, errors.New("close staged skill bundle")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := requireUnchangedDirectory(s.objectRoot, rootInfo, "skill revision object root"); err != nil {
		return result, err
	}
	if err := root.Rename(stageName, objectName); err != nil {
		if _, existsErr := root.Lstat(objectName); existsErr == nil {
			if _, verifyErr := readSkillBundleFromRoot(ctx, root, objectName, revision); verifyErr == nil {
				result.Duplicate = true
				return result, nil
			}
		}
		return result, fmt.Errorf("commit immutable skill bundle: %w", err)
	}
	committed = true
	if err := directoryHandle.Sync(); err != nil {
		return result, errors.New("sync skill revision object root")
	}
	if err := requireUnchangedDirectory(s.objectRoot, rootInfo, "skill revision object root"); err != nil {
		return result, err
	}
	return result, nil
}

func (s *RevisionBundleStore) Read(ctx context.Context, revision core.SkillRevision) (map[string][]byte, error) {
	if s == nil {
		return nil, errors.New("revision bundle store is required")
	}
	if err := revision.Validate(); err != nil {
		return nil, err
	}
	root, rootInfo, directoryHandle, err := s.openObjectRoot()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	defer directoryHandle.Close()
	if err := requireUnchangedDirectory(s.objectRoot, rootInfo, "skill revision object root"); err != nil {
		return nil, err
	}
	return readSkillBundleFromRoot(ctx, root, strings.TrimPrefix(revision.BundleDigest, "sha256:"), revision)
}

func (s *RevisionBundleStore) openObjectRoot() (*os.Root, os.FileInfo, *os.File, error) {
	validated, err := os.Lstat(s.objectRoot)
	if err != nil || !validated.IsDir() || validated.Mode()&os.ModeSymlink != 0 {
		return nil, nil, nil, errors.New("skill revision object root is invalid")
	}
	root, err := os.OpenRoot(s.objectRoot)
	if err != nil {
		return nil, nil, nil, errors.New("open skill revision object root")
	}
	handle, err := root.Open(".")
	if err != nil {
		root.Close()
		return nil, nil, nil, errors.New("open skill revision object root descriptor")
	}
	opened, err := handle.Stat()
	if err != nil || !opened.IsDir() || !os.SameFile(validated, opened) {
		handle.Close()
		root.Close()
		return nil, nil, nil, errors.New("skill revision object root changed while opening")
	}
	return root, opened, handle, nil
}

func validateSkillBundleContents(revision core.SkillRevision, contents map[string][]byte) error {
	if err := revision.Validate(); err != nil {
		return err
	}
	if len(contents) != len(revision.Files) || len(contents) > core.MaxSkillBundleFiles {
		return errors.New("skill bundle contents do not match declared files")
	}
	manifestFiles := append([]core.SkillBundleFile(nil), revision.Files...)
	sortSkillBundleFiles(manifestFiles)
	if skillBundleDigest(manifestFiles) != revision.BundleDigest {
		return errors.New("skill bundle manifest digest does not match revision")
	}
	for _, declared := range manifestFiles {
		content, exists := contents[declared.Path]
		if !exists {
			return fmt.Errorf("declared skill bundle file %q is missing", declared.Path)
		}
		if int64(len(content)) != declared.SizeBytes || int64(len(content)) > core.MaxSkillBundleFileBytes {
			return fmt.Errorf("skill bundle file %q size does not match", declared.Path)
		}
		if declared.Path == "SKILL.md" && len(content) > 12_000 {
			return errors.New("SKILL.md exceeds 12000 bytes")
		}
		digest := sha256.Sum256(content)
		if "sha256:"+hex.EncodeToString(digest[:]) != declared.Digest {
			return fmt.Errorf("skill bundle file %q digest does not match", declared.Path)
		}
	}
	return nil
}

func createSkillBundleStage(root *os.Root) (string, error) {
	for range 16 {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", errors.New("generate skill bundle stage name")
		}
		name := ".stage-" + hex.EncodeToString(random[:])
		if err := root.Mkdir(name, 0o700); err == nil {
			return name, nil
		} else if !errors.Is(err, fs.ErrExist) {
			return "", errors.New("create skill bundle stage")
		}
	}
	return "", errors.New("allocate skill bundle stage")
}

func writeStagedSkillBundle(ctx context.Context, stage *os.Root, revision core.SkillRevision, contents map[string][]byte) error {
	if err := stage.Mkdir(skillBundleDirectory, 0o700); err != nil {
		return errors.New("create staged bundle directory")
	}
	files := append([]core.SkillBundleFile(nil), revision.Files...)
	sortSkillBundleFiles(files)
	for _, declared := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := filepath.ToSlash(filepath.Join(skillBundleDirectory, declared.Path))
		if err := ensureRootDirectories(stage, filepath.ToSlash(filepath.Dir(name))); err != nil {
			return err
		}
		file, err := stage.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o400)
		if err != nil {
			return fmt.Errorf("create staged skill bundle file %q: %w", declared.Path, err)
		}
		if _, err := file.Write(contents[declared.Path]); err != nil {
			file.Close()
			return fmt.Errorf("write staged skill bundle file %q: %w", declared.Path, err)
		}
		if err := file.Sync(); err != nil {
			file.Close()
			return fmt.Errorf("sync staged skill bundle file %q: %w", declared.Path, err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("close staged skill bundle file %q: %w", declared.Path, err)
		}
	}
	manifest, err := json.Marshal(storedSkillBundleManifest{ManifestVersion: revision.ManifestVersion, BundleDigest: revision.BundleDigest, Files: files})
	if err != nil {
		return errors.New("encode skill bundle manifest")
	}
	manifest = append(manifest, '\n')
	file, err := stage.OpenFile(skillBundleManifestName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o400)
	if err != nil {
		return errors.New("create skill bundle manifest")
	}
	if _, err := file.Write(manifest); err != nil {
		file.Close()
		return errors.New("write skill bundle manifest")
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return errors.New("sync skill bundle manifest")
	}
	if err := file.Close(); err != nil {
		return errors.New("close skill bundle manifest")
	}
	return syncRootDirectories(stage)
}

func readSkillBundleFromRoot(ctx context.Context, root *os.Root, objectName string, revision core.SkillRevision) (map[string][]byte, error) {
	if len(objectName) != 64 || strings.ContainsAny(objectName, `/\\`) {
		return nil, errors.New("skill bundle object name is invalid")
	}
	object, err := root.OpenRoot(objectName)
	if err != nil {
		return nil, errors.New("open immutable skill bundle")
	}
	defer object.Close()
	if err := requireRootRegularFile(object, skillBundleManifestName); err != nil {
		return nil, err
	}
	rawManifest, err := object.ReadFile(skillBundleManifestName)
	if err != nil {
		return nil, errors.New("read skill bundle manifest")
	}
	var manifest storedSkillBundleManifest
	if err := json.Unmarshal(rawManifest, &manifest); err != nil {
		return nil, errors.New("decode skill bundle manifest")
	}
	expected := append([]core.SkillBundleFile(nil), revision.Files...)
	sortSkillBundleFiles(expected)
	if manifest.ManifestVersion != revision.ManifestVersion || manifest.BundleDigest != revision.BundleDigest || !equalSkillBundleFiles(manifest.Files, expected) {
		return nil, errors.New("stored skill bundle manifest does not match revision")
	}
	seen := make(map[string]struct{}, len(expected))
	err = fs.WalkDir(object.FS(), skillBundleDirectory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("stored skill bundle contains a symlink")
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("stored skill bundle contains a non-regular file")
		}
		relative := strings.TrimPrefix(filepath.ToSlash(path), skillBundleDirectory+"/")
		seen[relative] = struct{}{}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(seen) != len(expected) {
		return nil, errors.New("stored skill bundle file inventory does not match manifest")
	}
	contents := make(map[string][]byte, len(expected))
	for _, declared := range expected {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, exists := seen[declared.Path]; !exists {
			return nil, fmt.Errorf("stored skill bundle file %q is missing", declared.Path)
		}
		name := filepath.ToSlash(filepath.Join(skillBundleDirectory, declared.Path))
		if err := requireRootRegularFile(object, name); err != nil {
			return nil, err
		}
		content, err := object.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("read stored skill bundle file %q: %w", declared.Path, err)
		}
		contents[declared.Path] = content
	}
	if err := validateSkillBundleContents(revision, contents); err != nil {
		return nil, err
	}
	return contents, nil
}

func ensureRootDirectories(root *os.Root, path string) error {
	clean := filepath.ToSlash(filepath.Clean(path))
	if clean == "." || clean == "" {
		return nil
	}
	current := ""
	for _, part := range strings.Split(clean, "/") {
		if part == "" || part == "." || part == ".." {
			return errors.New("custody directory path is unsafe")
		}
		current = strings.TrimPrefix(current+"/"+part, "/")
		info, err := root.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			if err := root.Mkdir(current, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
				return errors.New("create custody directory")
			}
			info, err = root.Lstat(current)
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("custody directory is invalid")
		}
	}
	return nil
}

func requireRootRegularFile(root *os.Root, name string) error {
	current := ""
	parts := strings.Split(filepath.ToSlash(name), "/")
	for index, part := range parts {
		if part == "" || part == "." || part == ".." {
			return errors.New("stored skill bundle path is unsafe")
		}
		current = strings.TrimPrefix(current+"/"+part, "/")
		info, err := root.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("stored skill bundle path is invalid")
		}
		if index < len(parts)-1 && !info.IsDir() {
			return errors.New("stored skill bundle parent is not a directory")
		}
		if index == len(parts)-1 && !info.Mode().IsRegular() {
			return errors.New("stored skill bundle file is not regular")
		}
	}
	return nil
}

func makeStagedBundleReadOnly(root *os.Root) error {
	directories := make([]string, 0)
	err := fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("staged skill bundle contains a symlink")
		}
		if entry.IsDir() {
			directories = append(directories, path)
			return nil
		}
		if err := root.Chmod(path, 0o400); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return errors.New("secure staged skill bundle")
	}
	sort.Slice(directories, func(i, j int) bool { return strings.Count(directories[i], "/") > strings.Count(directories[j], "/") })
	for _, directory := range directories {
		if err := root.Chmod(directory, 0o500); err != nil {
			return errors.New("secure staged skill bundle directory")
		}
	}
	return nil
}

func removeSkillBundleStage(root *os.Root, stageName string) {
	stage, err := root.OpenRoot(stageName)
	if err == nil {
		directories := make([]string, 0)
		_ = fs.WalkDir(stage.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if entry.IsDir() {
				directories = append(directories, path)
				return nil
			}
			_ = stage.Chmod(path, 0o600)
			return nil
		})
		sort.Slice(directories, func(i, j int) bool { return strings.Count(directories[i], "/") > strings.Count(directories[j], "/") })
		for _, directory := range directories {
			_ = stage.Chmod(directory, 0o700)
		}
		_ = stage.Close()
	}
	_ = root.RemoveAll(stageName)
}

func syncRootDirectories(root *os.Root) error {
	directories := make([]string, 0)
	if err := fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			directories = append(directories, path)
		}
		return nil
	}); err != nil {
		return errors.New("enumerate staged skill bundle directories")
	}
	sort.Slice(directories, func(i, j int) bool { return strings.Count(directories[i], "/") > strings.Count(directories[j], "/") })
	for _, directory := range directories {
		handle, err := root.Open(directory)
		if err != nil {
			return errors.New("open staged skill bundle directory")
		}
		err = handle.Sync()
		closeErr := handle.Close()
		if err != nil || closeErr != nil {
			return errors.New("sync staged skill bundle directory")
		}
	}
	return nil
}

func requireUnchangedDirectory(path string, opened os.FileInfo, label string) error {
	current, err := os.Lstat(path)
	if err != nil || !current.IsDir() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, current) {
		return fmt.Errorf("%s changed during operation", label)
	}
	return nil
}

func sortSkillBundleFiles(files []core.SkillBundleFile) {
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
}

func skillBundleDigest(files []core.SkillBundleFile) string {
	ordered := append([]core.SkillBundleFile(nil), files...)
	sortSkillBundleFiles(ordered)
	hash := sha256.New()
	for _, file := range ordered {
		hash.Write([]byte(file.Path))
		hash.Write([]byte{0})
		hash.Write([]byte(file.Digest))
		hash.Write([]byte{0})
		hash.Write([]byte(strconv.FormatInt(file.SizeBytes, 10)))
		hash.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func equalSkillBundleFiles(left, right []core.SkillBundleFile) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
