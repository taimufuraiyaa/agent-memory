package auth

import (
	"context"
	"errors"
)

type CompositeAuthenticator struct{ authenticators []Authenticator }

func NewCompositeAuthenticator(authenticators ...Authenticator) *CompositeAuthenticator {
	return &CompositeAuthenticator{authenticators: authenticators}
}

func (a *CompositeAuthenticator) Verify(ctx context.Context, token string) (Identity, error) {
	for _, candidate := range a.authenticators {
		if candidate == nil {
			continue
		}
		identity, err := candidate.Verify(ctx, token)
		if err == nil {
			return identity, nil
		}
	}
	return Identity{}, errors.New("invalid bearer token")
}
