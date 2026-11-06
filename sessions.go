package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

type createSessionRequest struct {
	StartPage int    `json:"start_page"`
	EndPage   int    `json:"end_page"`
	ReadAt    string `json:"read_at"`
}

type session struct {
	SessionID int    `json:"session_id"`
	BookID    int    `json:"book_id"`
	StartPage int    `json:"start_page"`
	EndPage   int    `json:"end_page"`
	ReadAt    string `json:"read_at"`
	PagesRead int    `json:"pages_read"`
}

var errBookNotReading = errors.New("book is not in reading status")
var errPastLastPage = errors.New("end page is past the last page")

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

func (a *app) handleCreateSession(w http.ResponseWriter, r *http.Request) {
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
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	problems := map[string]string{}
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
	sess, err := insertSession(r.Context(), a.db, id, req)
	if err != nil {
		if errors.Is(err, errPastLastPage) {
			writeError(w, http.StatusBadRequest, "end page is past the last page")
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
		a.logger.Error("failed to create session", "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusCreated, sess)
}

func insertSession(ctx context.Context, db *sql.DB, id int64, req createSessionRequest) (session, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return session{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	var totalPages int
	var status string
	err = tx.QueryRowContext(ctx, `SELECT total_pages, status FROM books WHERE book_id = ?`, id).Scan(&totalPages, &status)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return session{}, errBookNotFound
		}
		return session{}, fmt.Errorf("load book: %w", err)
	}
	if req.EndPage > totalPages {
		return session{}, errPastLastPage
	}
	if status != "reading" {
		return session{}, errBookNotReading
	}

	var sess session
	err = tx.QueryRowContext(ctx, `INSERT INTO reading_sessions (book_id, start_page, end_page, read_at) VALUES (?, ?, ?, ?)
		RETURNING session_id, book_id, start_page, end_page, read_at`, id, req.StartPage, req.EndPage, req.ReadAt).
		Scan(&sess.SessionID, &sess.BookID, &sess.StartPage, &sess.EndPage, &sess.ReadAt)

	if err != nil {
		return session{}, fmt.Errorf("insert session: %w", err)
	}
	sess.PagesRead = sess.EndPage - sess.StartPage

	if req.EndPage == totalPages {
		if _, err = tx.ExecContext(ctx, `UPDATE books SET status = 'completed' WHERE book_id = ?`, id); err != nil {
			return session{}, fmt.Errorf("complete book: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return session{}, fmt.Errorf("commit: %w", err)
	}
	return sess, nil
}
