package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWebUIRoutesAreRemoved(t *testing.T) {
	mux := NewMux(&Service{})
	for _, path := range []string{"/dashboard/", "/dashboard/runtime.json", "/dashboard/assets/app.js", "/w/workspace/knowledge/notes", "/ops/dashboard"} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Errorf("removed frontend route %s still serves HTTP %d", path, response.Code)
		}
	}
}
