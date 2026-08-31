package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestSkillWorkerHealthSeparatesLivenessAndReadiness(t *testing.T) {
	var live, ready atomic.Bool
	live.Store(true)
	server := skillHealthServer(":0", &live, &ready)
	request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatal("live process failed liveness")
	}
	request = httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatal("unready process reported ready")
	}
	ready.Store(true)
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatal("ready process failed readiness")
	}
}

func TestSkillWorkerTelemetryExposesPrometheusMetrics(t *testing.T) {
	var live, ready atomic.Bool
	server := skillHealthServer(":0", &live, &ready)
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") == "" {
		t.Fatalf("metrics response code=%d content-type=%q", response.Code, response.Header().Get("Content-Type"))
	}
}
