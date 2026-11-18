package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConnectUsesLocalNetworkAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/zt/join" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["network_id"] != "0123456789abcdef" {
			t.Errorf("unexpected body: %v, %v", body, err)
		}
		_, _ = w.Write([]byte(`{"ok":true,"data":{"joined":true}}`))
	}))
	defer server.Close()
	var output bytes.Buffer
	if err := run([]string{"connect", "0123456789abcdef"}, &output, server.Client(), server.URL); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "status") {
		t.Fatal("connection response must prompt for readiness check")
	}
}

func TestInvalidNetworkDoesNotCallBackend(t *testing.T) {
	if err := run([]string{"connect", "not-a-network"}, &bytes.Buffer{}, http.DefaultClient, "http://127.0.0.1:1"); err == nil {
		t.Fatal("invalid network ID must fail")
	}
}

func TestStatusAndBackendFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/zt/health" {
			_, _ = w.Write([]byte(`{"ok":true,"data":{"node_ready":true,"networks":[]}}`))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"ok":false,"error":"engine unavailable"}`))
	}))
	defer server.Close()
	var output bytes.Buffer
	if err := run([]string{"status"}, &output, server.Client(), server.URL); err != nil || !strings.Contains(output.String(), `"node_ready": true`) {
		t.Fatalf("status: %s, %v", output.String(), err)
	}
	if err := run([]string{"connect", "0123456789abcdef"}, &output, server.Client(), server.URL); err == nil || !strings.Contains(err.Error(), "engine unavailable") {
		t.Fatalf("backend error not reported: %v", err)
	}
}
