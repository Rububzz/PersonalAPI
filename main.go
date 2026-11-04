package main

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

//go:embed schema.sql
var schema string

type app struct {
	db     *sql.DB
	logger *slog.Logger
}
type healthStatus struct {
	Status string `json:"status"`
}

type config struct {
	Port   string
	DBPath string
}

type createBookRequest struct {
	Title      string `json:"title"`
	Author     string `json:"author"`
	Edition    int    `json:"edition"`
	TotalPages int    `json:"total_pages"`
}

type book struct {
	BookID     int    `json:"book_id"`
	Title      string `json:"title"`
	Author     string `json:"author"`
	Edition    int    `json:"edition"`
	TotalPages int    `json:"total_pages"`
	Status     string `json:"status"`
	CreatedAt  string `json:"created_at"`
}

func loadConfig() config {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "reading.db"
	}
	return config{
		Port:   port,
		DBPath: dbPath,
	}
}

func (s *app) healthCheckfunc(w http.ResponseWriter, r *http.Request) {

	if err := s.db.PingContext(r.Context()); err != nil {
		s.logger.Error("failed ping database", "error", err)
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	writeJSON(w, http.StatusOK, healthStatus{Status: "ok"})

}

func writeJSON(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) error {
	return writeJSON(w, status, map[string]string{"error": msg})
}

func (a *app) addBooks(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req createBookRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}

	problems := map[string]string{}
	title := strings.TrimSpace(req.Title)
	author := strings.TrimSpace(req.Author)
	if req.Edition == 0 {
		req.Edition = 1
	}
	if title == "" {
		problems["title"] = "must not be empty"
	}
	if author == "" {
		problems["author"] = "must not be empty"
	}
	if req.Edition < 1 {
		problems["edition"] = "must be at least 1"
	}
	if req.TotalPages < 1 {
		problems["total_pages"] = "must be at least 1"
	}

	if len(problems) > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":  "validation failed",
			"fields": problems,
		})
		return
	}

	var b book
	err := a.db.QueryRowContext(r.Context(), `INSERT INTO books (title, author, edition, total_pages) VALUES (?, ?, ?, ?) 
	RETURNING book_id, title, author, edition, total_pages, status, created_at`,
		title, author, req.Edition, req.TotalPages).
		Scan(&b.BookID, &b.Title, &b.Author, &b.Edition, &b.TotalPages, &b.Status, &b.CreatedAt)

	if err != nil {
		var sqliteErr *sqlite.Error
		if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
			writeError(w, http.StatusConflict, "book already exists")
			return
		}
		a.logger.Error("error has occured", "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/books/%d", b.BookID))
	writeJSON(w, http.StatusCreated, b)

}

func run(logger *slog.Logger) error {
	mux := http.NewServeMux()
	c := loadConfig()

	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", c.DBPath)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a := &app{
		db:     db,
		logger: logger,
	}

	defer db.Close()

	_, err = db.Exec(schema)
	if err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}
	mux.HandleFunc("GET /health", a.healthCheckfunc)
	mux.HandleFunc("POST /books", a.addBooks)

	addr := ":" + c.Port
	logger.Info("listening on", "addr", addr)
	server := http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
	}
	serverChan := make(chan error, 1)
	go func() {
		serverChan <- server.ListenAndServe()
	}()
	select {
	case serverError := <-serverChan:
		return fmt.Errorf("server error: %w", serverError)
	case <-ctx.Done():
		logger.Info("Server shutting down")
		newCTX, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(newCTX)
	}
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("error has occurred", "error", err)
		os.Exit(1)
	}

}
