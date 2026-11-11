package main

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

//go:embed schema.sql
var schema string

type config struct {
	Port   string
	DBPath string
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

func openDB(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_txlock=immediate&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	_, err = db.Exec(schema)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	return db, nil

}

func (a *app) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", a.handleHealth)
	mux.HandleFunc("POST /books", a.handleCreateBook)
	mux.HandleFunc("GET /books", a.handleListBooks)
	mux.HandleFunc("GET /books/{id}", a.handleGetBook)
	mux.HandleFunc("POST /books/{id}/sessions", a.handleCreateSession)
	mux.HandleFunc("GET /books/{id}/sessions", a.handleListSessions)
	mux.HandleFunc("GET /books/{id}/progress", a.handleGetBookProgress)
	return mux
}

func run(logger *slog.Logger) error {
	c := loadConfig()

	db, err := openDB(c.DBPath)
	if err != nil {
		return fmt.Errorf("openDB: %w", err)
	}
	defer db.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a := &app{
		db:     db,
		logger: logger,
	}

	mux := a.routes()

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
		logger.Info("server shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("server stopped with an error", "error", err)
		os.Exit(1)
	}
}
