package localoidc

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProviderPublishesDiscoveryMintsSyntheticTokenAndRotatesWithOverlap(t *testing.T) {
	provider, err := New(Config{
		Issuer: "http://oidc:8082", Audience: "agent-memory-local",
		Subject: "local-oidc|member", Email: "member@oidc.local.invalid", DisplayName: "Local OIDC Member",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := provider.Handler()

	discovery := request(t, handler, http.MethodGet, "/.well-known/openid-configuration")
	var metadata map[string]any
	decodeJSON(t, discovery, &metadata)
	if metadata["issuer"] != "http://oidc:8082" || metadata["jwks_uri"] != "http://oidc:8082/keys" {
		t.Fatalf("unexpected discovery metadata: %#v", metadata)
	}

	firstToken := tokenFrom(t, request(t, handler, http.MethodGet, "/token"))
	firstKid := tokenKid(t, firstToken)
	assertSyntheticClaims(t, firstToken)
	request(t, handler, http.MethodPost, "/rotate")
	secondToken := tokenFrom(t, request(t, handler, http.MethodGet, "/token"))
	secondKid := tokenKid(t, secondToken)
	if firstKid == secondKid {
		t.Fatal("rotation did not change the signing key ID")
	}

	keysResponse := request(t, handler, http.MethodGet, "/keys")
	var keys struct {
		Keys []struct {
			Kid string `json:"kid"`
		} `json:"keys"`
	}
	decodeJSON(t, keysResponse, &keys)
	if len(keys.Keys) != 2 || keys.Keys[0].Kid != secondKid || keys.Keys[1].Kid != firstKid {
		t.Fatalf("rotation overlap keys = %+v", keys.Keys)
	}
}

func TestProviderRejectsUnsafeConfiguration(t *testing.T) {
	valid := Config{Issuer: "http://oidc:8082", Audience: "agent-memory-local", Subject: "local|member", Email: "member@local.invalid", DisplayName: "Member"}
	cases := map[string]Config{
		"issuer":   {Audience: valid.Audience, Subject: valid.Subject, Email: valid.Email},
		"audience": {Issuer: valid.Issuer, Subject: valid.Subject, Email: valid.Email},
		"subject":  {Issuer: valid.Issuer, Audience: valid.Audience, Email: valid.Email},
		"email":    {Issuer: valid.Issuer, Audience: valid.Audience, Subject: valid.Subject, Email: "not-an-email"},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := New(cfg); err == nil {
				t.Fatal("expected configuration rejection")
			}
		})
	}
}

func request(t *testing.T, handler http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(method, path, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("%s %s status=%d body=%s", method, path, response.Code, response.Body.String())
	}
	return response
}

func decodeJSON(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatal(err)
	}
}

func tokenFrom(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Token string `json:"token"`
	}
	decodeJSON(t, response, &body)
	if strings.Count(body.Token, ".") != 2 {
		t.Fatal("provider did not return a compact JWT")
	}
	return body.Token
}

func tokenKid(t *testing.T, token string) string {
	t.Helper()
	parts := strings.Split(token, ".")
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	var header struct {
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		t.Fatal(err)
	}
	return header.Kid
}

func assertSyntheticClaims(t *testing.T, token string) {
	t.Helper()
	parts := strings.Split(token, ".")
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatal(err)
	}
	if claims["sub"] != "local-oidc|member" || claims["email"] != "member@oidc.local.invalid" || claims["email_verified"] != true {
		t.Fatalf("unexpected synthetic claims: %#v", claims)
	}
}
