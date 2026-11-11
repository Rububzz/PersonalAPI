package main

import (
	"encoding/json"
	"math"
	"net/http"
	"testing"
)

func TestCreateBook(t *testing.T) {
	a := newTestApp(t)
	w := doRequest(t, a, http.MethodPost, "/books", `{"title":"The Great Gatsby","author":"F. Scott Fitzgerald","total_pages":180}`)

	if w.Code != http.StatusCreated {
		t.Fatalf("want status 201 Created, got %d, body: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Location"); got != "/books/1" {
		t.Errorf("Location = %q, want %q", got, "/books/1")
	}
	if w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("want Content-Type application/json, got %s", w.Header().Get("Content-Type"))
	}

	var resp book
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Title != "The Great Gatsby" {
		t.Errorf("want title 'The Great Gatsby', got %s", resp.Title)
	}
	if resp.Author != "F. Scott Fitzgerald" {
		t.Errorf("want author 'F. Scott Fitzgerald', got %s", resp.Author)
	}
	if resp.TotalPages != 180 {
		t.Errorf("want total_pages 180, got %d", resp.TotalPages)
	}
	if resp.Status != "reading" {
		t.Errorf("want status 'reading', got %s", resp.Status)
	}
	if resp.Edition != 1 {
		t.Errorf("want edition 1, got %d", resp.Edition)
	}
}

func TestCreateBookErrors(t *testing.T) {
	test := []struct {
		name string
		body string
		want int
	}{
		{
			name: "missing title",
			body: `{"author":"F. Scott Fitzgerald","total_pages":180}`,
			want: http.StatusBadRequest,
		},
		{
			name: "missing author",
			body: `{"title":"The Great Gatsby","total_pages":180}`,
			want: http.StatusBadRequest,
		},
		{
			name: "missing total_pages",
			body: `{"title":"The Great Gatsby","author":"F. Scott Fitzgerald"}`,
			want: http.StatusBadRequest,
		},
		{
			name: "invalid total_pages",
			body: `{"title":"The Great Gatsby","author":"F. Scott Fitzgerald","total_pages":-1}`,
			want: http.StatusBadRequest,
		},
		{
			name: "duplicate book",
			body: `{"title":"The Great Gatsby","author":"F. Scott Fitzgerald","total_pages":180}`,
			want: http.StatusConflict,
		},
		{
			name: "invalid JSON",
			body: `{"title":"The Great Gatsby","author":"F. Scott Fitzgerald","total_pages":180`,
			want: http.StatusBadRequest,
		},
		{
			name: "no body",
			body: ``,
			want: http.StatusBadRequest,
		},
		{
			name: "unknown field",
			body: `{"title":"The Great Gatsby","author":"F. Scott Fitzgerald","total_pages":180, "unknown":"field"}`,
			want: http.StatusBadRequest,
		},
	}

	for _, tt := range test {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestApp(t)
			doRequest(t, a, http.MethodPost, "/books", `{"title":"The Great Gatsby","author":"F. Scott Fitzgerald","total_pages":180}`)
			w := doRequest(t, a, http.MethodPost, "/books", tt.body)
			if w.Code != tt.want {
				t.Fatalf("want status %d, got %d, body: %s", tt.want, w.Code, w.Body.String())
			}
		})
	}
}

func TestGetBook(t *testing.T) {
	tests := []struct {
		name string
		path string
		want int
	}{
		{name: "existing book", path: "/books/1", want: http.StatusOK},
		{name: "non-existing book", path: "/books/999", want: http.StatusNotFound},
		{name: "invalid book ID", path: "/books/abc", want: http.StatusBadRequest},
		{name: "book ID zero", path: "/books/0", want: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestApp(t)
			doRequest(t, a, http.MethodPost, "/books", `{"title":"The Great Gatsby","author":"F. Scott Fitzgerald","total_pages":180}`)
			w := doRequest(t, a, http.MethodGet, tt.path, "")
			if w.Code != tt.want {
				t.Fatalf("want status %d, got %d, body: %s", tt.want, w.Code, w.Body.String())
			}
		})
	}
}

func TestCreateSession(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
		want int
	}{
		{name: "create session for existing book", path: "/books/1/sessions", body: `{"start_page": 0, "end_page": 20}`, want: http.StatusCreated},
		{name: "create session for non-existing book", path: "/books/999/sessions", body: `{"start_page": 0, "end_page": 20}`, want: http.StatusNotFound},
		{name: "invalid book ID", path: "/books/abc/sessions", body: `{"start_page": 0, "end_page" : 20}`, want: http.StatusBadRequest},
		{name: "page range exceeds total pages", path: "/books/1/sessions", body: `{"start_page": 0, "end_page": 200}`, want: http.StatusBadRequest},
		{name: "start page greater than end page", path: "/books/1/sessions", body: `{"start_page": 20, "end_page": 10}`, want: http.StatusBadRequest},
		{name: "bad read_at", path: "/books/1/sessions", body: `{"start_page": 0, "end_page": 20, "read_at": "yesterday"}`, want: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestApp(t)
			doRequest(t, a, http.MethodPost, "/books", `{"title":"The Great Gatsby","author":"F. Scott Fitzgerald","total_pages":180}`)
			w := doRequest(t, a, http.MethodPost, tt.path, tt.body)
			if w.Code != tt.want {
				t.Fatalf("want status %d, got %d, body: %s", tt.want, w.Code, w.Body.String())
			}
		})
	}
}

func TestSessionOnCompletedBook(t *testing.T) {
	a := newTestApp(t)

	// arrange 1: create a 180-page book (it gets id 1)
	doRequest(t, a, http.MethodPost, "/books", `{"title":"The Great Gatsby","author":"F. Scott Fitzgerald","total_pages":180}`)

	// arrange 2: read to the last page, which marks the book completed
	if w := doRequest(t, a, http.MethodPost, "/books/1/sessions", `{"start_page": 0, "end_page": 180}`); w.Code != http.StatusCreated {
		t.Fatalf("arrange: finishing the book: want 201, got %d, body: %s", w.Code, w.Body.String())
	}

	// act + assert: one more session on the completed book must be refused
	w := doRequest(t, a, http.MethodPost, "/books/1/sessions", `{"start_page": 0, "end_page": 20}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("want status %d, got %d, body: %s", http.StatusConflict, w.Code, w.Body.String())
	}
}

func TestCreateSessionValidationShape(t *testing.T) {
	a := newTestApp(t)
	doRequest(t, a, http.MethodPost, "/books", `{"title":"The Great Gatsby","author":"F. Scott Fitzgerald","total_pages":180}`)

	w := doRequest(t, a, http.MethodPost, "/books/1/sessions", `{"start_page": -1, "end_page": 20, "read_at": "yesterday"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want status %d, got %d, body: %s", http.StatusBadRequest, w.Code, w.Body.String())
	}

	var got errorResponse
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Error != "validation failed" {
		t.Errorf("error = %q, want %q", got.Error, "validation failed")
	}
	for _, key := range []string{"start_page", "read_at"} {
		if _, ok := got.Fields[key]; !ok {
			t.Errorf("fields = %v, want a %q entry", got.Fields, key)
		}
	}
}
func TestListSessions(t *testing.T) {
	a := newTestApp(t)
	tests := []string{
		`{"start_page": 20, "end_page": 50, "read_at": "2026-11-10T20:00:00Z"}`,
		`{"start_page": 0, "end_page": 20, "read_at": "2026-11-09T20:00:00Z"}`,
	}

	// arrange: one 180-page book with two sessions, logged newest first
	doRequest(t, a, http.MethodPost, "/books", `{"title":"The Great Gatsby","author":"F. Scott Fitzgerald","total_pages":180}`)
	for _, body := range tests {
		if w := doRequest(t, a, http.MethodPost, "/books/1/sessions", body); w.Code != http.StatusCreated {
			t.Fatalf("arrange: want 201, got %d, body: %s", w.Code, w.Body.String())
		}
	}

	// act
	w := doRequest(t, a, http.MethodGet, "/books/1/sessions", "")

	// assert
	if w.Code != http.StatusOK {
		t.Fatalf("want status %d, got %d, body: %s", http.StatusOK, w.Code, w.Body.String())
	}
	var got []session
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d sessions, want 2: %+v", len(got), got)
	}
	if got[0].StartPage != 0 || got[1].StartPage != 20 {
		t.Errorf("wrong order, want oldest read_at first: %+v", got)
	}
	if got[0].PagesRead != 20 {
		t.Errorf("pages_read = %d, want 20", got[0].PagesRead)
	}
	if got[1].PagesRead != 30 {
		t.Errorf("pages_read = %d, want 30", got[1].PagesRead)
	}
}

func TestListSessionsErrors(t *testing.T) {
	tests := []struct {
		name string
		path string
		want int
	}{
		{name: "bad id", path: "/books/abc/sessions", want: http.StatusBadRequest},
		{name: "unknown book", path: "/books/999/sessions", want: http.StatusNotFound},
		{name: "book with no sessions", path: "/books/1/sessions", want: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestApp(t)
			doRequest(t, a, http.MethodPost, "/books", `{"title":"The Great Gatsby","author":"F. Scott Fitzgerald","total_pages":180}`)
			w := doRequest(t, a, http.MethodGet, tt.path, "")
			if w.Code != tt.want {
				t.Fatalf("want status %d, got %d, body: %s", tt.want, w.Code, w.Body.String())
			}
		})
	}
}

func TestBookProgress(t *testing.T) {
	a := newTestApp(t)

	// arrange: one 180-page book with two sessions, logged newest first
	doRequest(t, a, http.MethodPost, "/books", `{"title":"The Great Gatsby","author":"F. Scott Fitzgerald","total_pages":180}`)
	tests := []string{
		`{"start_page": 0, "end_page": 120, "read_at": "2026-11-10T20:00:00Z"}`,
		`{"start_page": 30, "end_page": 50, "read_at": "2026-11-09T20:00:00Z"}`,
	}
	for _, tt := range tests {
		if w := doRequest(t, a, http.MethodPost, "/books/1/sessions", tt); w.Code != http.StatusCreated {
			t.Fatalf("arrange: want 201, got %d, body: %s", w.Code, w.Body.String())
		}
	}

	// act
	w := doRequest(t, a, http.MethodGet, "/books/1/progress", "")

	// assert
	if w.Code != http.StatusOK {
		t.Fatalf("want status %d, got %d, body: %s", http.StatusOK, w.Code, w.Body.String())
	}

	if w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("want Content-Type application/json, got %s", w.Header().Get("Content-Type"))
	}

	var got bookProgress

	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got.Sessions != 2 || got.CurrentPage != 120 || got.PagesRead != 140 {
		t.Errorf("got %+v, want sessions 2, current_page 120, pages_read 140", got)
	}
	if math.Abs(got.PercentComplete-66.67) > 0.01 {
		t.Errorf("percent_complete = %v, want about 66.67", got.PercentComplete)
	}
	if got.LastReadAt == nil || *got.LastReadAt != "2026-11-10T20:00:00Z" {
		t.Errorf("last_read_at = %v, want 2026-11-10T20:00:00Z", got.LastReadAt)
	}
}

func TestBookProgressErrors(t *testing.T) {
	tests := []struct {
		name string
		path string
		want int
	}{
		{name: "bad id", path: "/books/abc/progress", want: http.StatusBadRequest},
		{name: "unknown book", path: "/books/999/progress", want: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestApp(t)
			doRequest(t, a, http.MethodPost, "/books", `{"title":"The Great Gatsby","author":"F. Scott Fitzgerald","total_pages":180}`)
			w := doRequest(t, a, http.MethodGet, tt.path, "")
			if w.Code != tt.want {
				t.Fatalf("want status %d, got %d, body: %s", tt.want, w.Code, w.Body.String())
			}
		})
	}
}

func TestBookProgressNoSessions(t *testing.T) {
	a := newTestApp(t)

	// arrange: one 180-page book with no sessions
	doRequest(t, a, http.MethodPost, "/books", `{"title":"The Great Gatsby","author":"F. Scott Fitzgerald","total_pages":180}`)

	// act
	w := doRequest(t, a, http.MethodGet, "/books/1/progress", "")

	// assert
	if w.Code != http.StatusOK {
		t.Fatalf("want status %d, got %d, body: %s", http.StatusOK, w.Code, w.Body.String())
	}

	var got bookProgress
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got.Sessions != 0 || got.CurrentPage != 0 || got.PagesRead != 0 || got.PercentComplete != 0 || got.LastReadAt != nil {
		t.Errorf("got %+v, want sessions 0, current_page 0, pages_read 0, percent_complete 0, last_read_at nil", got)
	}
}
