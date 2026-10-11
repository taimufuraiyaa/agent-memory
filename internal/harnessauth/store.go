package harnessauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	fileName      = "harness-authority.json"
	lockName      = "harness-authority.lock"
	credentialDir = "credentials"
	maxFileBytes  = 1 << 20
	lockStaleAge  = 30 * time.Second
	lockWait      = 3 * time.Second
)

// dirPath returns the private credential directory. It never follows a symlinked
// base or credential directory and requires 0700 once the directory exists.
func (a *Authority) dirPath(create bool) (string, error) {
	base := a.base
	if create {
		if err := os.MkdirAll(base, 0o700); err != nil {
			return "", errStorage("cannot create data directory")
		}
	}
	info, err := os.Lstat(base)
	if errors.Is(err, os.ErrNotExist) && !create {
		return filepath.Join(base, credentialDir), nil
	}
	if err != nil || !info.IsDir() {
		return "", errStorage("unsafe data directory")
	}
	dir := filepath.Join(base, credentialDir)
	if create {
		if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return "", errStorage("cannot create credential directory")
		}
	}
	info, err = os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) && !create {
		return dir, nil
	}
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return "", errStorage("unsafe credential directory")
	}
	return dir, nil
}

func validateFile(info os.FileInfo) error {
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() > maxFileBytes {
		return errStorage("unsafe authority file")
	}
	return nil
}

// load reads the authority state fresh from disk so revocation, rotation and
// disabling take effect on the very next verification. A missing file is the
// default disabled state; anything unsafe or unreadable fails closed.
func (a *Authority) load() (state, error) {
	dir, err := a.dirPath(false)
	if err != nil {
		return state{}, err
	}
	path := filepath.Join(dir, fileName)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return state{SchemaVersion: schemaVersion}, nil
	}
	if err != nil {
		return state{}, errStorage("cannot inspect authority file")
	}
	if err := validateFile(info); err != nil {
		return state{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return state{}, errStorage("cannot read authority file")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || validateFile(opened) != nil || !os.SameFile(info, opened) {
		return state{}, errStorage("unsafe authority file")
	}
	content, err := io.ReadAll(io.LimitReader(file, maxFileBytes+1))
	if err != nil || len(content) > maxFileBytes {
		return state{}, errStorage("cannot read authority file")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var loaded state
	if decoder.Decode(&loaded) != nil || decoder.Decode(new(any)) != io.EOF {
		return state{}, errStorage("malformed authority file")
	}
	if err := loaded.validate(); err != nil {
		return state{}, err
	}
	return loaded, nil
}

// mutate applies one change under an exclusive cross-process lock, so a revoke
// is never lost to a concurrent mint, rotate or disable.
func (a *Authority) mutate(ctx context.Context, change func(*state) error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	dir, err := a.dirPath(true)
	if err != nil {
		return err
	}
	unlock, err := lockDir(ctx, dir)
	if err != nil {
		return err
	}
	defer unlock()
	current, err := a.load()
	if err != nil {
		return err
	}
	if err := change(&current); err != nil {
		return err
	}
	current.SchemaVersion = schemaVersion
	return persist(dir, current)
}

func lockDir(ctx context.Context, dir string) (func(), error) {
	path := filepath.Join(dir, lockName)
	deadline := time.Now().Add(lockWait)
	for {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = file.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, errStorage("cannot lock authority file")
		}
		if info, statErr := os.Lstat(path); statErr == nil && time.Since(info.ModTime()) > lockStaleAge {
			_ = os.Remove(path)
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, errStorage("authority file is busy")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func persist(dir string, s state) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return errStorage("cannot encode authority file")
	}
	if len(data) > maxFileBytes {
		return errStorage("authority file too large")
	}
	path := filepath.Join(dir, fileName)
	if info, err := os.Lstat(path); err == nil {
		if err := validateFile(info); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errStorage("cannot inspect authority file")
	}
	temp, err := os.CreateTemp(dir, ".harness-authority-*.tmp")
	if err != nil {
		return errStorage("cannot create authority file")
	}
	defer os.Remove(temp.Name())
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return errStorage("cannot secure authority file")
	}
	if _, err := temp.Write(append(data, '\n')); err != nil {
		_ = temp.Close()
		return errStorage("cannot write authority file")
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return errStorage("cannot sync authority file")
	}
	if err := temp.Close(); err != nil {
		return errStorage("cannot close authority file")
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		return errStorage("cannot replace authority file")
	}
	return nil
}
