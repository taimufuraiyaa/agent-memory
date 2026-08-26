package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestGraphWorkerHealthReflectsRuntimeReadiness(t *testing.T) {
	var ready atomic.Bool
	server := healthServer(":0", &ready, nil)
	request := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatal("unready graph worker reported ready")
	}
	ready.Store(true)
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatal("ready graph worker failed readiness")
	}
}
