package evidencepublish

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const temporaryNameAttempts = 16

// JSON publishes one synced, mode-0600 JSON file without overwriting an
// existing destination. All mutations are anchored to one opened parent root.
func JSON(path string, value any, temporaryPattern string) error {
	return jsonWithHooks(path, value, temporaryPattern, nil, nil)
}

func jsonWithHooks(path string, value any, temporaryPattern string, afterRoot, afterLink func()) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("receipt path is required")
	}
	directory := filepath.Dir(path)
	base := filepath.Base(path)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return errors.New("receipt filename is invalid")
	}
	prefix := strings.TrimSuffix(temporaryPattern, "*")
	if prefix == "" || filepath.Base(prefix) != prefix {
		return errors.New("temporary receipt pattern is invalid")
	}

	validated, err := os.Lstat(directory)
	if err != nil || !validated.IsDir() || validated.Mode()&os.ModeSymlink != 0 {
		return errors.New("receipt directory is invalid")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return errors.New("open receipt directory")
	}
	defer root.Close()
	directoryHandle, err := root.Open(".")
	if err != nil {
		return errors.New("open receipt directory descriptor")
	}
	defer directoryHandle.Close()
	opened, err := directoryHandle.Stat()
	if err != nil || !opened.IsDir() || !os.SameFile(validated, opened) {
		return errors.New("receipt directory changed while opening")
	}
	if afterRoot != nil {
		afterRoot()
	}
	if err := requireDirectoryPath(directory, opened); err != nil {
		return err
	}
	if _, err := root.Lstat(base); err == nil {
		return errors.New("receipt destination already exists")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return errors.New("inspect receipt destination")
	}

	temporaryName, temporary, err := createTemporary(root, prefix)
	if err != nil {
		return err
	}
	defer root.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return errors.New("secure temporary receipt")
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		temporary.Close()
		return errors.New("encode receipt")
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return errors.New("sync temporary receipt")
	}
	if err := temporary.Close(); err != nil {
		return errors.New("close temporary receipt")
	}
	if err := requireDirectoryPath(directory, opened); err != nil {
		return err
	}
	if err := root.Link(temporaryName, base); err != nil {
		return fmt.Errorf("link receipt: %w", err)
	}
	linked := true
	defer func() {
		if linked {
			_ = root.Remove(base)
		}
	}()
	if afterLink != nil {
		afterLink()
	}
	if err := requireDirectoryPath(directory, opened); err != nil {
		return err
	}
	if err := directoryHandle.Sync(); err != nil {
		return errors.New("sync receipt directory")
	}
	if err := requireDirectoryPath(directory, opened); err != nil {
		return err
	}
	linked = false
	return nil
}

func requireDirectoryPath(path string, opened os.FileInfo) error {
	current, err := os.Lstat(path)
	if err != nil || !current.IsDir() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, current) {
		return errors.New("receipt directory changed during publication")
	}
	return nil
}

func createTemporary(root *os.Root, prefix string) (string, *os.File, error) {
	for range temporaryNameAttempts {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", nil, errors.New("generate temporary receipt name")
		}
		name := prefix + hex.EncodeToString(random[:])
		file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return name, file, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", nil, errors.New("create temporary receipt")
		}
	}
	return "", nil, errors.New("allocate temporary receipt name")
}
