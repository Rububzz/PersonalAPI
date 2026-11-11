package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Helper
func newTestApp(t *testing.T) *app {
	t.Helper()
	db, err := openDB(filepath.Join(t.TempDir(), "testing.db"))
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	return &app{
		db:     db,
		logger: logger,
	}
}

type errorResponse struct {
	Error  string            `json:"error"`
	Fields map[string]string `json:"fields"`
}

func doRequest(t *testing.T, a *app, method, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	return w
}

func TestHealth(t *testing.T) {
	a := newTestApp(t)
	var resp healthStatus
	w := doRequest(t, a, http.MethodGet, "/health", "")
	if w.Code != http.StatusOK {
		t.Fatalf("want status 200 OK, got %d", w.Code)
	}
	if w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("want Content-Type application/json, got %s", w.Header().Get("Content-Type"))
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("want status 'ok', got %s", resp.Status)
	}
}
