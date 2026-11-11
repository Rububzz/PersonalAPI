package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

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

type bookProgress struct {
	BookID          int     `json:"book_id"`
	Sessions        int     `json:"sessions"`
	PagesRead       int     `json:"pages_read"`
	CurrentPage     int     `json:"current_page"`
	LastReadAt      *string `json:"last_read_at"`
	Status          string  `json:"status"`
	TotalPages      int     `json:"total_pages"`
	PercentComplete float64 `json:"percent_complete"`
}

var errBookExists = errors.New("book already exists")
var errBookNotFound = errors.New("book not found")

func (a *app) handleCreateBook(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req createBookRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
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
		problems["total_pages"] = "must be at least 1"
	}

	if len(problems) > 0 {
		writeValidationError(w, problems)
		return
	}
	b, err := insertBook(r.Context(), a.db, req)
	if err != nil {
		if errors.Is(err, errBookExists) {
			writeError(w, http.StatusConflict, "book already exists")
			return
		}
		a.logger.Error("failed to insert book", "error", err)
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
		return book{}, fmt.Errorf("insert book: %w", err)
	}
	return b, nil
}

func (a *app) handleListBooks(w http.ResponseWriter, r *http.Request) {
	books, err := listBooks(r.Context(), a.db)
	if err != nil {
		a.logger.Error("failed to list books", "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, books)
}

func listBooks(ctx context.Context, db *sql.DB) ([]book, error) {
	books := make([]book, 0)
	rows, err := db.QueryContext(ctx, `SELECT book_id, title, author, edition, total_pages, status, created_at FROM books ORDER BY book_id`)
	if err != nil {
		return nil, fmt.Errorf("query books: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var b book
		if err := rows.Scan(&b.BookID, &b.Title, &b.Author, &b.Edition, &b.TotalPages, &b.Status, &b.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan book: %w", err)
		}
		books = append(books, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate books: %w", err)
	}
	return books, nil
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
		a.logger.Error("failed to get book", "error", err)
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

func (a *app) handleGetBookProgress(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "invalid book id")
		return
	}
	progress, err := getBookProgress(r.Context(), a.db, id)
	if err != nil {
		if errors.Is(err, errBookNotFound) {
			writeError(w, http.StatusNotFound, "book not found")
			return
		}
		a.logger.Error("failed to get book progress", "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, progress)
}

func getBookProgress(ctx context.Context, db *sql.DB, id int64) (bookProgress, error) {
	var bp bookProgress
	var lastRead sql.NullString
	b, err := getBook(ctx, db, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return bookProgress{}, errBookNotFound
		}
		return bookProgress{}, fmt.Errorf("get book: %w", err)
	}
	bp.BookID = b.BookID
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(MAX(end_page), 0), COALESCE(SUM(end_page - start_page), 0), 
	MAX(read_at), COALESCE(MAX(end_page) * 100.0 / ?, 0)  
	FROM reading_sessions WHERE book_id = ?`, b.TotalPages, b.BookID).Scan(&bp.Sessions, &bp.CurrentPage, &bp.PagesRead, &lastRead, &bp.PercentComplete); err != nil {
		return bookProgress{}, fmt.Errorf("query book progress: %w", err)
	}
	if lastRead.Valid {
		bp.LastReadAt = &lastRead.String
	}
	bp.Status = b.Status
	bp.TotalPages = b.TotalPages
	return bp, nil
}
