package clientauth

import (
	"errors"
	"testing"

	keyring "github.com/zalando/go-keyring"
)

func TestOSKeyringStoresAndDeletesHostedTokenWithoutFiles(t *testing.T) {
	keyring.MockInit()
	store := OSKeyring{}
	if err := store.Set("test-profile", "am_sk_private"); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get("test-profile")
	if err != nil || got != "am_sk_private" {
		t.Fatalf("token=%q err=%v", got, err)
	}
	if err := store.Delete("test-profile"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("test-profile"); !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("deleted token error=%v", err)
	}
}
