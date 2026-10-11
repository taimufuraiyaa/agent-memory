package agentmemory

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientRequiresExplicitLocalMode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true,"data":{"id":"ok"}}`)
	}))
	defer server.Close()
	if _, err := New(Config{BaseURL: server.URL}); err == nil {
		t.Fatal("implicit mode was accepted")
	}
	if _, err := New(Config{Mode: Mode("remote"), BaseURL: server.URL}); err == nil {
		t.Fatal("removed remote mode was accepted")
	}
	if _, err := New(Config{Mode: ModeLocal, BaseURL: server.URL}); err != nil {
		t.Fatal(err)
	}
}

func TestClientMapsLocalRequests(t *testing.T) {
	var request *http.Request
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request = r.Clone(r.Context())
		body, _ = io.ReadAll(r.Body)
		_, _ = io.WriteString(w, `{"ok":true,"data":{"id":"ok"}}`)
	}))
	defer server.Close()
	client, err := New(Config{Mode: ModeLocal, BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	value := MemoryWrite{WorkspaceID: "workspace", Type: "semantic", Content: "fact"}
	if _, err := client.WriteMemory(context.Background(), value, "idempotency-key"); err != nil {
		t.Fatal(err)
	}
	if request == nil || request.URL.Path != "/api/v1/memories/write" || request.Header.Get("Idempotency-Key") != "idempotency-key" {
		t.Fatalf("local request=%v", request)
	}
	if string(body) != `{"content":"fact","keywords":null,"type":"semantic","workspace":"workspace"}` {
		t.Fatalf("local request body=%s", body)
	}
}
