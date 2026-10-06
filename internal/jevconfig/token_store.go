// Package jevconfig owns local Jev credentials, separate from harness contracts.
package jevconfig

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxTokenBytes = 4096
const credentialDir = "credentials"
const tokenFilename = "jev-access-token"

type TokenStore struct{ BaseDir string }

func NewTokenStore(baseDir string) *TokenStore { return &TokenStore{BaseDir: baseDir} }

// Configured reveals presence only. It never reads or returns the token.
func (s *TokenStore) Configured(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	path, err := s.path(false)
	if err != nil {
		return false, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, errors.New("cannot inspect Jev credential")
	}
	if err := validateFile(info); err != nil {
		return false, err
	}
	return true, nil
}

// Load is reserved for a local Jev adapter. Callers must not log its result.
func (s *TokenStore) Load(ctx context.Context) (string, bool, error) {
	configured, err := s.Configured(ctx)
	if err != nil || !configured {
		return "", false, err
	}
	path, err := s.path(false)
	if err != nil {
		return "", false, err
	}
	file, err := os.Open(path)
	if err != nil {
		return "", false, errors.New("cannot read Jev credential")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || validateFile(info) != nil {
		return "", false, errors.New("unsafe Jev credential file")
	}
	pathInfo, err := os.Lstat(path)
	if err != nil || validateFile(pathInfo) != nil || !os.SameFile(info, pathInfo) {
		return "", false, errors.New("unsafe Jev credential file")
	}
	content, err := io.ReadAll(io.LimitReader(file, maxTokenBytes+1))
	if err != nil || len(content) > maxTokenBytes {
		return "", false, errors.New("cannot read Jev credential")
	}
	token := string(content)
	if validateToken(token) != nil {
		return "", false, errors.New("invalid stored Jev credential")
	}
	return token, true, nil
}

func (s *TokenStore) Save(ctx context.Context, token string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateToken(token); err != nil {
		return err
	}
	path, err := s.path(true)
	if err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if err := validateFile(info); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("cannot inspect Jev credential")
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".jev-token-*.tmp")
	if err != nil {
		return errors.New("cannot create Jev credential")
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return errors.New("cannot secure Jev credential")
	}
	if _, err := io.WriteString(file, token); err != nil {
		_ = file.Close()
		return errors.New("cannot write Jev credential")
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return errors.New("cannot sync Jev credential")
	}
	if err := file.Close(); err != nil {
		return errors.New("cannot close Jev credential")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return errors.New("cannot replace Jev credential")
	}
	return nil
}

func (s *TokenStore) Clear(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	configured, err := s.Configured(ctx)
	if err != nil || !configured {
		return err
	}
	path, err := s.path(false)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return errors.New("cannot remove Jev credential")
	}
	return nil
}

func (s *TokenStore) path(create bool) (string, error) {
	base := strings.TrimSpace(s.BaseDir)
	if base == "" {
		return "", errors.New("Jev credential directory is not configured")
	}
	if create {
		if err := os.MkdirAll(base, 0o700); err != nil {
			return "", errors.New("cannot create Jev credential base directory")
		}
	}
	info, err := os.Lstat(base)
	if errors.Is(err, os.ErrNotExist) && !create {
		return filepath.Join(base, credentialDir, tokenFilename), nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("unsafe Jev credential base directory")
	}
	dir := filepath.Join(base, credentialDir)
	if create {
		if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return "", errors.New("cannot create Jev credential directory")
		}
	}
	info, err = os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) && !create {
		return filepath.Join(dir, tokenFilename), nil
	}
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return "", errors.New("unsafe Jev credential directory")
	}
	return filepath.Join(dir, tokenFilename), nil
}

func validateFile(info os.FileInfo) error {
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() < 1 || info.Size() > maxTokenBytes {
		return errors.New("unsafe Jev credential file")
	}
	return nil
}

func validateToken(token string) error {
	if token == "" || len(token) > maxTokenBytes {
		return fmt.Errorf("Jev access token must be 1-%d bytes", maxTokenBytes)
	}
	for _, char := range []byte(token) {
		if char < '!' || char > '~' {
			return errors.New("Jev access token must use printable ASCII without spaces")
		}
	}
	return nil
}
