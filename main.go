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
	"strconv"
	"strings"
	"syscall"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

//go:embed schema.sql
var schema string
var errBookExists = errors.New("book already exists")
var errPageOverlimit = errors.New("end page over total")
var errBookNotFound = errors.New("book Not found")
var errBookNotReading = errors.New("book is not in reading status")

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

type createSessionRequest struct {
	StartPage int    `json:"start_page"`
	EndPage   int    `json:"end_page"`
	ReadAt    string `json:"read_at"`
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

type session struct {
	SessionID int    `json:"session_id"`
	BookID    int    `json:"book_id"`
	StartPage int    `json:"start_page"`
	EndPage   int    `json:"end_page"`
	ReadAt    string `json:"read_at"`
	PagesRead int    `json:"pages_read"`
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
	req.Title = strings.TrimSpace(req.Title)
	req.Author = strings.TrimSpace(req.Author)
	if req.Edition == 0 {
		req.Edition = 1
	}
	if req.Title == "" {
		problems["title"] = "must not be empty"
	}
	if req.Author == "" {
		problems["author"] = "must not be empty"
	}
	if req.Edition < 1 {
		problems["edition"] = "must be at least 1"
	}
	if req.TotalPages < 1 {
		problems["totalPages"] = "must be at least 1"
	}

	if len(problems) > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":  "validation failed",
			"fields": problems,
		})
		return
	}
	b, err := insertBook(r.Context(), a.db, req)
	if err != nil {
		if errors.Is(err, errBookExists) {
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

func insertBook(ctx context.Context, db *sql.DB, req createBookRequest) (book, error) {
	var b book
	err := db.QueryRowContext(ctx, `INSERT INTO books (title, author, edition, total_pages) VALUES (?, ?, ?, ?) 
	RETURNING book_id, title, author, edition, total_pages, status, created_at`,
		req.Title, req.Author, req.Edition, req.TotalPages).
		Scan(&b.BookID, &b.Title, &b.Author, &b.Edition, &b.TotalPages, &b.Status, &b.CreatedAt)

	if err != nil {
		var sqliteErr *sqlite.Error
		if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
			return book{}, errBookExists
		}
		return book{}, fmt.Errorf("insert book error: %w", err)
	}
	return b, nil
}

func (a *app) handleGetBookList(w http.ResponseWriter, r *http.Request) {
	books, err := listBooks(r.Context(), a.db)
	if err != nil {
		a.logger.Error("error has occured", "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, books)
}

func listBooks(ctx context.Context, db *sql.DB) ([]book, error) {
	var booklist = make([]book, 0)
	rows, err := db.QueryContext(ctx, `SELECT book_id, title, author, edition, total_pages, status, created_at FROM books ORDER BY book_id`)
	if err != nil {
		return nil, fmt.Errorf("query books failed: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var b book
		if err := rows.Scan(&b.BookID, &b.Title, &b.Author, &b.Edition, &b.TotalPages, &b.Status, &b.CreatedAt); err != nil {
			return nil, fmt.Errorf("book scan fail: %w", err)
		}
		booklist = append(booklist, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}
	return booklist, nil
}

func (a *app) handleGetBook(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "invalid book id")
		return
	}
	b, err := getBook(r.Context(), a.db, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "book not found")
			return
		}
		a.logger.Error("error has occured", "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func getBook(ctx context.Context, db *sql.DB, id int64) (book, error) {
	var b book
	err := db.QueryRowContext(ctx, `SELECT book_id, title, author, edition, total_pages, status, created_at FROM books WHERE book_id = ?`, id).
		Scan(&b.BookID, &b.Title, &b.Author, &b.Edition, &b.TotalPages, &b.Status, &b.CreatedAt)
	if err != nil {
		return book{}, err
	}
	return b, nil
}

func (a *app) handlePostReadingSess(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "invalid book id")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req createSessionRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	problems := make(map[string]string, 0)
	if req.StartPage < 0 {
		problems["start_page"] = "must be at least 0"
	}
	if req.EndPage < 1 {
		problems["end_page"] = "must be at least 1"
	}
	if req.StartPage >= req.EndPage {
		problems["start_page"] = "start must be less than end"
	}
	readAt, err := normalizeReadAt(req.ReadAt)
	if err != nil {
		problems["read_at"] = "must be an RFC 3339 time, like 2026-11-04T21:30:00+08:00"
	}
	req.ReadAt = readAt
	if len(problems) > 0 {
		writeJSON(w, http.StatusBadRequest, problems)
		return
	}
	sess, err := createSession(r.Context(), a.db, id, req)
	if err != nil {
		if errors.Is(err, errPageOverlimit) {
			writeError(w, http.StatusBadRequest, "page over limit")
			return
		}
		if errors.Is(err, errBookNotFound) {
			writeError(w, http.StatusNotFound, "book not found")
			return
		}
		if errors.Is(err, errBookNotReading) {
			writeError(w, http.StatusConflict, "book is not in reading status")
			return
		}
		a.logger.Error("error in creating session", "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusCreated, sess)
}

func normalizeReadAt(raw string) (string, error) {
	if raw == "" {
		return time.Now().UTC().Format(time.RFC3339), nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return "", fmt.Errorf("parse read_at: %w", err)
	}
	return t.UTC().Format(time.RFC3339), nil
}

func createSession(ctx context.Context, db *sql.DB, id int64, req createSessionRequest) (session, error) {
	txn, err := db.BeginTx(ctx, nil)
	if err != nil {
		return session{}, fmt.Errorf("begin: %w", err)
	}
	defer txn.Rollback()

	var total_page int
	var status string
	err = txn.QueryRowContext(ctx, `SELECT total_pages, status FROM books WHERE book_id = ?`, id).Scan(&total_page, &status)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return session{}, errBookNotFound
		}
		return session{}, fmt.Errorf("load books: %w", err)
	}
	if total_page < req.EndPage {
		return session{}, errPageOverlimit
	}
	if status != "reading" {
		return session{}, errBookNotReading
	}

	var sess session
	err = txn.QueryRowContext(ctx, `INSERT into reading_sessions (book_id, start_page, end_page, read_at) VALUES (?, ?, ?, ?) 
		RETURNING session_id, book_id, start_page, end_page, read_at`, id, req.StartPage, req.EndPage, req.ReadAt).
		Scan(&sess.SessionID, &sess.BookID, &sess.StartPage, &sess.EndPage, &sess.ReadAt)

	if err != nil {
		return session{}, fmt.Errorf("insert error: %w", err)
	}
	sess.PagesRead = sess.EndPage - sess.StartPage

	if req.EndPage == total_page {
		if _, err = txn.ExecContext(ctx, `UPDATE books set status = 'completed' where book_id = ?`, id); err != nil {
			return session{}, fmt.Errorf("update status to complete Failed: %w", err)
		}
	}

	if err := txn.Commit(); err != nil {
		return session{}, fmt.Errorf("commit error: %w", err)
	}
	return sess, nil
}

func run(logger *slog.Logger) error {
	mux := http.NewServeMux()
	c := loadConfig()

	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_txlock=immediate&_pragma=busy_timeout(5000)", c.DBPath)

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
	mux.HandleFunc("GET /books", a.handleGetBookList)
	mux.HandleFunc("GET /books/{id}", a.handleGetBook)
	mux.HandleFunc("POST /books/{id}/sessions", a.handlePostReadingSess)

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
