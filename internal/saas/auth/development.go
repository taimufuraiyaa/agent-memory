package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"strings"
)

// DevelopmentAuthenticator is a single-user local identity emulator. Config
// validation prevents it from being enabled in production.
type DevelopmentAuthenticator struct {
	token       string
	subjectID   string
	email       string
	displayName string
}

func NewDevelopmentAuthenticator(token, subjectID, email, displayName string) (*DevelopmentAuthenticator, error) {
	a := &DevelopmentAuthenticator{
		token: strings.TrimSpace(token), subjectID: strings.TrimSpace(subjectID),
		email: strings.ToLower(strings.TrimSpace(email)), displayName: strings.TrimSpace(displayName),
	}
	if a.token == "" || a.subjectID == "" || a.email == "" {
		return nil, errors.New("development identity is incomplete")
	}
	return a, nil
}

func (a *DevelopmentAuthenticator) Verify(_ context.Context, bearerToken string) (Identity, error) {
	provided := []byte(strings.TrimSpace(bearerToken))
	expected := []byte(a.token)
	if len(provided) != len(expected) || subtle.ConstantTimeCompare(provided, expected) != 1 {
		return Identity{}, errors.New("invalid bearer token")
	}
	return Identity{SubjectID: a.subjectID, SessionID: "development-session"}, nil
}

type VerifiedProfile struct {
	SubjectID   string
	Email       string
	DisplayName string
}

func (a *DevelopmentAuthenticator) Profile(ctx context.Context, bearerToken string) (VerifiedProfile, error) {
	identity, err := a.Verify(ctx, bearerToken)
	if err != nil {
		return VerifiedProfile{}, err
	}
	return VerifiedProfile{SubjectID: identity.SubjectID, Email: a.email, DisplayName: a.displayName}, nil
}
