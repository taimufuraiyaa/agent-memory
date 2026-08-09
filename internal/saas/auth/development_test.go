package auth

import (
	"context"
	"testing"
)

func TestDevelopmentAuthenticatorVerifiesOnlyConfiguredToken(t *testing.T) {
	authenticator, err := NewDevelopmentAuthenticator("configured-token", "provider|one", "ONE@EXAMPLE.TEST", "One")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authenticator.Verify(context.Background(), "wrong-token"); err == nil {
		t.Fatal("wrong token was accepted")
	}
	profile, err := authenticator.Profile(context.Background(), "configured-token")
	if err != nil || profile.SubjectID != "provider|one" || profile.Email != "one@example.test" {
		t.Fatalf("Profile() = %+v, %v", profile, err)
	}
}
