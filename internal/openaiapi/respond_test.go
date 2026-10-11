package openaiapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestToolRequestsAreBoundedBeforeAnythingIsSent(t *testing.T) {
	sent := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { sent++ }))
	defer server.Close()
	c := NewHarnessClient(server.URL, time.Second)
	params := map[string]any{"type": "object"}
	many := make([]Tool, 65)
	for i := range many {
		many[i] = Tool{Name: fmt.Sprintf("t%d", i), Parameters: params}
	}
	for name, tools := range map[string][]Tool{
		"a bad name":     {{Name: "../x", Parameters: params}},
		"a digit first":  {{Name: "1x", Parameters: params}},
		"no parameters":  {{Name: "ok"}},
		"too many tools": many,
	} {
		if _, err := c.Respond(context.Background(), "k", "gpt-x", Request{Input: "hi", MaxOutputTokens: 10, Tools: tools}); err == nil {
			t.Errorf("%s was sent", name)
		}
	}
	if sent != 0 {
		t.Fatalf("%d invalid requests reached the service", sent)
	}
}
