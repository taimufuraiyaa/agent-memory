package application

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type nativeGraphState struct {
	Scope       core.GraphScope   `json:"scope"`
	Revision    string            `json:"revision"`
	BuildDigest string            `json:"build_digest"`
	JobDir      string            `json:"job_dir"`
	CreatedAt   time.Time         `json:"created_at"`
	Files       map[string]string `json:"files"`
}

func (w *NativeGraphWorker) statePath(scope core.GraphScope, revision string) string {
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(scope.TenantID+"\x00"+scope.WorkspaceID+"\x00"+revision))
	return filepath.Join(w.configuration.JobRoot, "native-state-"+id.String()+".json")
}

func (w *NativeGraphWorker) saveState(scope core.GraphScope, revision, jobDir, digest string) error {
	if !safeNativeGraphJobDir(w.configuration.JobRoot, jobDir) {
		return fmt.Errorf("unsafe native graph state directory")
	}
	files, err := nativeGraphStateFiles(jobDir, w.configuration.MaxDiskBytes)
	if err != nil {
		return err
	}
	state := nativeGraphState{Scope: scope, Revision: revision, BuildDigest: digest, JobDir: jobDir, CreatedAt: w.now().UTC(), Files: files}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(w.statePath(scope, revision), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func (w *NativeGraphWorker) loadState(scope core.GraphScope, revision, digest string) (string, error) {
	data, err := readNativeGraphFile(w.statePath(scope, revision), 4<<20)
	if err != nil {
		return "", err
	}
	var state nativeGraphState
	if err = json.Unmarshal(data, &state); err != nil {
		return "", err
	}
	if state.Scope != scope || state.Revision != revision || state.BuildDigest != digest || !safeNativeGraphJobDir(w.configuration.JobRoot, state.JobDir) {
		return "", fmt.Errorf("incompatible native graph base state")
	}
	files, err := nativeGraphStateFiles(state.JobDir, w.configuration.MaxDiskBytes)
	if err != nil {
		return "", err
	}
	if len(files) != len(state.Files) {
		return "", fmt.Errorf("native graph base state changed")
	}
	for name, hash := range files {
		if state.Files[name] != hash {
			return "", fmt.Errorf("native graph base state changed")
		}
	}
	return state.JobDir, nil
}

func safeNativeGraphJobDir(root, path string) bool {
	return filepath.IsAbs(path) && filepath.Dir(filepath.Clean(path)) == filepath.Clean(root) && strings.HasPrefix(filepath.Base(path), ".graph-job-")
}

func nativeGraphStateFiles(jobDir string, limit int64) (map[string]string, error) {
	files := map[string]string{}
	var bytes int64
	parquet := false
	for _, directory := range []string{"output", "vector_store"} {
		root := filepath.Join(jobDir, directory)
		if _, err := os.Lstat(root); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("symlink in graph state")
			}
			if entry.IsDir() {
				if filepath.Base(path) == "normalized" {
					return filepath.SkipDir
				}
				return nil
			}
			if !entry.Type().IsRegular() {
				return fmt.Errorf("nonregular graph state")
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			bytes += info.Size()
			if bytes > limit {
				return fmt.Errorf("graph state exceeds disk limit")
			}
			hash, err := hashNativeGraphFile(path, limit)
			if err != nil {
				return err
			}
			name, err := filepath.Rel(jobDir, path)
			if err != nil {
				return err
			}
			files[name] = hash
			parquet = parquet || strings.HasSuffix(name, ".parquet")
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	if !parquet {
		return nil, fmt.Errorf("raw adapter tables are unavailable")
	}
	data, err := readNativeGraphFile(filepath.Join(jobDir, "output", "normalized", "artifact-manifest.json"), 1<<20)
	if err != nil {
		return nil, err
	}
	files[filepath.Join("output", "normalized", "artifact-manifest.json")] = nativeGraphHash(data)
	return files, nil
}

// prepareNativeGraphBase copies the verified immutable base into a new private
// job directory. Python never mutates the active base directory.
func prepareNativeGraphBase(jobDir, baseDir string, request map[string]any, limit int64) error {
	if !safeNativeGraphJobDir(filepath.Dir(jobDir), baseDir) {
		return fmt.Errorf("graph base directory is outside the job root")
	}
	files, err := nativeGraphStateFiles(baseDir, limit)
	if err != nil {
		return err
	}
	for name := range files {
		if name == filepath.Join("output", "normalized", "artifact-manifest.json") {
			continue
		}
		destination := filepath.Join(jobDir, name)
		if err = os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return err
		}
		if err = copyNativeGraphFile(filepath.Join(baseDir, name), destination, files[name], limit); err != nil {
			return err
		}
	}
	manifest, err := readNativeGraphFile(filepath.Join(baseDir, "output", "normalized", "artifact-manifest.json"), 1<<20)
	if err != nil {
		return err
	}
	basePath := filepath.Join(jobDir, "base-manifest.json")
	if err = os.WriteFile(basePath, manifest, 0o600); err != nil {
		return err
	}
	request["base_manifest_path"] = basePath
	request["base_manifest_sha256"] = strings.TrimPrefix(nativeGraphHash(manifest), "sha256:")
	delete(request, "native_base_job_dir")
	return nil
}

func hashNativeGraphFile(path string, limit int64) (string, error) {
	file, err := openNativeGraphStateFile(path, limit)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(file, limit+1))
	if err != nil {
		return "", err
	}
	if n > limit {
		return "", fmt.Errorf("graph state exceeds disk limit")
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func openNativeGraphStateFile(path string, limit int64) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("graph state file is unsafe")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		file.Close()
		return nil, fmt.Errorf("graph state changed while opening")
	}
	return file, nil
}

func copyNativeGraphFile(source, destination, expected string, limit int64) error {
	input, err := openNativeGraphStateFile(source, limit)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(output, hash), io.LimitReader(input, limit+1))
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n > limit || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != expected {
		return fmt.Errorf("graph base changed while copying")
	}
	return nil
}

// Active and previous raw revisions remain available for updates and rollback.
// Older private projections are removed after 24h, without touching other
// workspaces or arbitrary paths stored in malformed state files.
func (w *NativeGraphWorker) pruneStates(scope core.GraphScope, protected map[string]bool) error {
	entries, err := os.ReadDir(w.configuration.JobRoot)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	protectedDirs := map[string]bool{}
	for revision := range protected {
		data, readErr := readNativeGraphFile(w.statePath(scope, revision), 4<<20)
		if readErr != nil {
			continue
		}
		var state nativeGraphState
		if json.Unmarshal(data, &state) == nil && state.Scope == scope {
			protectedDirs[state.JobDir] = true
		}
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "native-state-") || !strings.HasSuffix(entry.Name(), ".json") || !entry.Type().IsRegular() {
			continue
		}
		path := filepath.Join(w.configuration.JobRoot, entry.Name())
		data, readErr := readNativeGraphFile(path, 4<<20)
		if readErr != nil {
			continue
		}
		var state nativeGraphState
		if json.Unmarshal(data, &state) != nil || state.Scope != scope || protected[state.Revision] || state.CreatedAt.IsZero() || w.now().Sub(state.CreatedAt) < 24*time.Hour || !safeNativeGraphJobDir(w.configuration.JobRoot, state.JobDir) || protectedDirs[state.JobDir] {
			continue
		}
		if err = os.RemoveAll(state.JobDir); err != nil {
			return err
		}
		if err = os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}
