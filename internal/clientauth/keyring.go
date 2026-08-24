// Package clientauth stores hosted credentials in the operating-system keyring.
package clientauth

import (
	"errors"
	"strings"

	keyring "github.com/zalando/go-keyring"
)

const serviceName = "agent-memory-hosted"

var ErrCredentialNotFound = errors.New("hosted credential was not found in the OS keyring")

type Store interface {
	Set(profile, token string) error
	Get(profile string) (string, error)
	Delete(profile string) error
}

type OSKeyring struct{}

func (OSKeyring) Set(profile, token string) error {
	profile, token = strings.TrimSpace(profile), strings.TrimSpace(token)
	if profile == "" || token == "" {
		return errors.New("hosted profile and token are required")
	}
	return keyring.Set(serviceName, profile, token)
}

func (OSKeyring) Get(profile string) (string, error) {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return "", errors.New("hosted profile is required")
	}
	token, err := keyring.Get(serviceName, profile)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrCredentialNotFound
	}
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(token) == "" {
		return "", ErrCredentialNotFound
	}
	return token, nil
}

func (OSKeyring) Delete(profile string) error {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return errors.New("hosted profile is required")
	}
	err := keyring.Delete(serviceName, profile)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}
