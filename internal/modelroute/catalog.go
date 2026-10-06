// Package modelroute owns a host-scoped allowlist of models eligible for Jev choice.
// Catalog entries are user configuration, not proof of account access.
package modelroute

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const FileName = "model-catalog.json"

type Candidate struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

type Catalog struct {
	Version int                    `json:"version"`
	Hosts   map[string][]Candidate `json:"hosts"`
}

func Load(dataDir string) (Catalog, error) {
	path := filepath.Join(dataDir, FileName)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() > 32768 {
		return Catalog{}, errors.New("model catalog is missing or unsafe")
	}
	file, err := os.Open(path)
	if err != nil {
		return Catalog{}, errors.New("cannot read model catalog")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return Catalog{}, errors.New("model catalog changed while opening")
	}
	content, err := io.ReadAll(io.LimitReader(file, 32769))
	if err != nil || len(content) > 32768 {
		return Catalog{}, errors.New("cannot read model catalog")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var catalog Catalog
	if decoder.Decode(&catalog) != nil || decoder.Decode(new(any)) != io.EOF || catalog.Version != 1 || len(catalog.Hosts) == 0 || len(catalog.Hosts) > 3 {
		return Catalog{}, errors.New("invalid model catalog")
	}
	for host, candidates := range catalog.Hosts {
		if host != "claude" && host != "chatgpt" && host != "openai_api" {
			return Catalog{}, errors.New("unknown model host")
		}
		if len(candidates) < 2 || len(candidates) > 16 {
			return Catalog{}, errors.New("invalid model candidate count")
		}
		seen := make(map[string]bool, len(candidates))
		for _, candidate := range candidates {
			if !validID(candidate.ID) || seen[candidate.ID] || !validDescription(candidate.Description) || len(candidate.ID)+len(candidate.Description)+2 > 250 {
				return Catalog{}, errors.New("invalid model candidate")
			}
			seen[candidate.ID] = true
		}
	}
	return catalog, nil
}

func (c Catalog) Candidates(host string) ([]Candidate, error) {
	if host != "claude" && host != "chatgpt" && host != "openai_api" {
		return nil, errors.New("unknown model host")
	}
	candidates := c.Hosts[host]
	if len(candidates) < 2 {
		return nil, errors.New("model host is not configured")
	}
	return append([]Candidate(nil), candidates...), nil
}

func validID(id string) bool {
	if id == "" || len(id) > 100 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_.:/", r)) {
			return false
		}
	}
	return true
}

func validDescription(description string) bool {
	if description == "" || len(description) > 180 {
		return false
	}
	for _, r := range description {
		if r < ' ' || r == '\x7f' {
			return false
		}
	}
	return true
}
