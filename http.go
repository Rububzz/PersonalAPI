package main

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
)

type app struct {
	db     *sql.DB
	logger *slog.Logger
}

type healthStatus struct {
	Status string `json:"status"`
}

func writeJSON(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) error {
	return writeJSON(w, status, map[string]string{"error": msg})
}

func writeValidationError(w http.ResponseWriter, problems map[string]string) error {
	return writeJSON(w, http.StatusBadRequest, map[string]any{
		"error":  "validation failed",
		"fields": problems,
	})
}

func (a *app) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := a.db.PingContext(r.Context()); err != nil {
		a.logger.Error("failed to ping database", "error", err)
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	writeJSON(w, http.StatusOK, healthStatus{Status: "ok"})
}
